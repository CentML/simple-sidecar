package webhook

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/yaml"
)

var (
	runtimeScheme     = runtime.NewScheme()
	codecs            = serializer.NewCodecFactory(runtimeScheme)
	deserializer      = codecs.UniversalDeserializer()
	webhookInjectPath = "/inject"
)

var ignoredNamespaces = []string{
	metav1.NamespaceSystem,
	metav1.NamespacePublic,
}

const (
	admissionWebhookAnnotationInjectKey = "simple-sidecar.centml.ai/inject"
	admissionWebhookAnnotationStatusKey = "simple-sidecar.cemtml.ai/status"
)

// Config is the struct used to parse injection config items for Simple Sidecar. The InitContainers,
// Containers, Volumes, and EnvVars fields are arrays of Kubernetes objects that will be added to
// the pod spec.
type Config struct {

	// InitContainers - inject one or more initContainers into the pod spec.
	InitContainers []corev1.Container

	// Containers - inject one or more containers into the pod spec.
	Containers []corev1.Container

	// ExistingContainerConfig - configuration for injecting into the pre-existing containers.
	ExistingContainerConfig
}

// ExistingContainerConfig provides configuration for injecting into the pre-existing containers.
// This is useful for utilizing the functionality of injected containers
type ExistingContainerConfig struct {
	// Volumes - inject one or more volumes into pre-existing pod specs.
	Volumes []corev1.Volume

	// EnvVars - inject one or more environment variables into pre-existing pod specs.
	EnvVars []corev1.EnvVar

	// VolumeMounts - inject one or more volume mounts into pre-existing pod specs.
	// BEFORE sidecar injection.
	VolumeMounts []corev1.VolumeMount
}

// MultiConfig is a map of Config objects. This allows for multiple named configurations
// to be loaded from a single configuration file. The name of the configuration is the key,
// it is used to determine which configuration to use when injecting sidecars.
type MultiConfig map[string]Config

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// LoadConfig loads the configuration from the specified file and returns a MultiConfig object.
func LoadConfig(configFile string) (cfg MultiConfig, err error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// WebhookServer contains the configuration for the webhook server. It's used as a receiver for various
// methods such as Start and Stop.
type WebhookServer struct {
	sidecarConfigs  MultiConfig
	server          *http.Server
	certPEM, keyPEM string
	infoLogger      *log.Logger
	warningLogger   *log.Logger
	errorLogger     *log.Logger
}

// WebhookServerConfig is the configuration for the webhook server. It contains the port to listen on,
// the path to the certificate and key files, the MultiConfig object containing the sidecar configurations,
// and the loggers for info, warning, and error messages.
type WebhookServerConfig struct {
	Port           int
	CertPEM        string
	KeyPEM         string
	SidecarConfigs MultiConfig
	InfoLogger     *log.Logger
	ErrorLogger    *log.Logger
	WarnLogger     *log.Logger
}

// NewWebhookServer creates a new WebhookServer object with the specified configuration.
func NewWebhookServer(cfg *WebhookServerConfig) *WebhookServer {

	whsvr := &WebhookServer{
		sidecarConfigs: cfg.SidecarConfigs,
		server: &http.Server{
			Addr: fmt.Sprintf(":%v", cfg.Port),
			TLSConfig: &tls.Config{
				// each request we retrieve the certs incase they have been rotated
				// this could be a bit smarter and only reload the certs if they have changed
				// for clusters without extreme churn this should be fine
				GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
					cert, err := tls.LoadX509KeyPair(cfg.CertPEM, cfg.KeyPEM)
					if err != nil {
						return nil, err
					}
					return &cert, nil
				},
			},
		},
		infoLogger:    cfg.InfoLogger,
		warningLogger: cfg.WarnLogger,
		errorLogger:   cfg.ErrorLogger,
	}

	// define http server and server handler
	mux := http.NewServeMux()
	mux.HandleFunc(webhookInjectPath, whsvr.Serve)
	whsvr.server.Handler = mux

	return whsvr
}

// Start method for webhook server. It blocks until the server is stopped.
func (whs *WebhookServer) Start() error {
	whs.infoLogger.Printf("Starting webhook server...\n")
	return whs.server.ListenAndServeTLS(whs.certPEM, whs.keyPEM)
}

// Stop method for webhook server. It stops the server gracefully.
func (whs *WebhookServer) Stop() {
	whs.server.Shutdown(context.Background())
}

// mutationRequired determines whether a mutation is required for the specified pod and if so which mutation to use
func (whs *WebhookServer) mutationRequired(ignoredList []string, metadata *metav1.ObjectMeta) (bool, string) {
	// skip special kubernete system namespaces
	for _, namespace := range ignoredList {
		if metadata.Namespace == namespace {
			whs.infoLogger.Printf("Skip mutation for %v for it's in special namespace:%v", metadata.Name, metadata.Namespace)
			return false, ""
		}
	}

	annotations := metadata.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	whs.infoLogger.Printf("Annotations: %v", annotations)

	// check if mutation has already occurred
	status := annotations[admissionWebhookAnnotationStatusKey]

	required, prevInj, mut := false, false, ""
	if strings.ToLower(status) == "injected" {
		prevInj = true
		required = false
	} else if val, ok := annotations[admissionWebhookAnnotationInjectKey]; ok {
		required = true
		mut = val
	}

	whs.infoLogger.Printf("Mutation policy for %v/%v: previously injected: %v required:%v, mutation: %s", metadata.Namespace, metadata.Name, prevInj, required, mut)
	return required, mut
}

// addContainer adds the container to the target containers
func (whs *WebhookServer) addContainer(target, added []corev1.Container, basePath string) (patch []patchOperation) {

	// if there's nothing to add, then return
	if len(added) == 0 {
		return patch
	}

	if len(target) == 0 {
		// If the target array is empty, add an empty array to the base path first
		patch = append(patch, patchOperation{
			Op:    "add",
			Path:  basePath,
			Value: []corev1.Container{},
		})
	}
	for _, add := range added {
		patch = append(patch, patchOperation{
			Op:    "add",
			Path:  basePath + "/-",
			Value: add,
		})
	}
	return patch
}

// addVolume adds the specified volumes to the target list of volumes
func (whs *WebhookServer) addVolume(target []corev1.Volume, added []corev1.Volume, basePath string) (patch []patchOperation) {

	// if there's nothing to add, then return
	if len(added) == 0 {
		return patch
	}

	if len(target) == 0 {
		// Create an add operation to initialize volumes with an empty array
		initOp := patchOperation{
			Op:    "add",
			Path:  basePath,
			Value: []corev1.Volume{},
		}
		patch = append(patch, initOp)
	}

	for _, add := range added {
		op := patchOperation{
			Op:    "add",
			Path:  basePath + "/-",
			Value: add,
		}
		patch = append(patch, op)
	}

	return patch
}

// patchFriendly replaces any / with ~1 in a string for use in a JSON patch
func patchFriendly(str string) string {
	return strings.ReplaceAll(str, "/", "~1")
}

// updateAnnotations updates/adds annotations
func (whs *WebhookServer) updateAnnotations(target map[string]string, added map[string]string) (patch []patchOperation) {

	// if there's no existing annotations, we need to start by creating an empty map
	if target == nil {
		target = map[string]string{}
		patch = append(patch, patchOperation{
			Op:    "add",
			Path:  "/metadata/annotations",
			Value: target,
		})
	}

	// either add or replace the annotation depending on whether it already exists
	for key, value := range added {
		if _, ok := target[key]; !ok {
			patch = append(patch, patchOperation{
				Op:    "add",
				Path:  "/metadata/annotations/" + patchFriendly(key),
				Value: value,
			})
		} else {
			patch = append(patch, patchOperation{
				Op:    "replace",
				Path:  "/metadata/annotations/" + patchFriendly(key),
				Value: value,
			})
		}
	}
	return patch
}

// addVolumeMounts adds volume mounts to the containers in the given pod
func (whs *WebhookServer) addVolumeMounts(pod *corev1.Pod, vms []corev1.VolumeMount) (patch []patchOperation) {

	// if there's nothing to add, then return
	if len(vms) == 0 {
		return patch
	}

	for i := range pod.Spec.Containers {
		// Check if volumeMounts field exists and is not empty for the container
		if len(pod.Spec.Containers[i].VolumeMounts) == 0 {
			// Create an add operation to initialize volumeMounts with an empty array
			initOp := patchOperation{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/volumeMounts", i),
				Value: []corev1.VolumeMount{},
			}
			patch = append(patch, initOp)
		}
		for _, vm := range vms {

			op := patchOperation{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/volumeMounts/-", i),
				Value: vm,
			}
			patch = append(patch, op)
		}
	}
	return patch
}

// addEnvVars adds environment variables to the containers in the given pod
func (whs *WebhookServer) addEnvVars(pod *corev1.Pod, envVars []corev1.EnvVar) (patch []patchOperation) {

	// no env vars to add, return
	if len(envVars) == 0 {
		return patch
	}

	// add the envVar for the existing containers
	for i := range pod.Spec.Containers {

		// Add an empty env field first if it doesn't exist
		if len(pod.Spec.Containers[i].Env) == 0 {
			op := patchOperation{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/env", i),
				Value: []corev1.EnvVar{},
			}
			patch = append(patch, op)
		}

		// Add the env vars
		for _, envVar := range envVars {

			op := patchOperation{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/env/-", i),
				Value: envVar,
			}
			whs.infoLogger.Printf("addEnvVars: op=%v\n", op)
			patch = append(patch, op)
		}
	}

	return patch
}

// createPatch creates a JSON patch for the pod using the sidecar configuration and annotations
func (whs *WebhookServer) createPatch(pod *corev1.Pod, sidecarConfig Config, annotations map[string]string) ([]byte, error) {

	cbytes, err := yaml.Marshal(sidecarConfig)
	if err != nil {
		return nil, err
	}
	whs.infoLogger.Printf("createPatch: sidecarConfig=%s\n", string(cbytes))
	var patch []patchOperation

	patch = append(patch, whs.addVolumeMounts(pod, sidecarConfig.VolumeMounts)...)
	patch = append(patch, whs.addEnvVars(pod, sidecarConfig.EnvVars)...)
	patch = append(patch, whs.addContainer(pod.Spec.InitContainers, sidecarConfig.InitContainers, "/spec/initContainers")...)
	patch = append(patch, whs.addContainer(pod.Spec.Containers, sidecarConfig.Containers, "/spec/containers")...)
	patch = append(patch, whs.addVolume(pod.Spec.Volumes, sidecarConfig.Volumes, "/spec/volumes")...)
	patch = append(patch, whs.updateAnnotations(pod.Annotations, annotations)...)

	return json.Marshal(patch)
}

// mutate is the main mutation function for the webhook server. It determines whether a mutation is required
// for the specified pod and if so, which mutation to use. It then creates a patch for the pod using the sidecar
// configuration and annotations.
func (whs *WebhookServer) mutate(ar *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := ar.Request
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		whs.warningLogger.Printf("Could not unmarshal raw object: %v", err)
		return &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	whs.infoLogger.Printf("AdmissionReview for Kind=%v, Namespace=%v Name=%v (%v) UID=%v patchOperation=%v UserInfo=%v",
		req.Kind, req.Namespace, req.Name, pod.Name, req.UID, req.Operation, req.UserInfo)

	// determine whether to perform mutation
	required, mut := whs.mutationRequired(ignoredNamespaces, &pod.ObjectMeta)
	if !required {
		whs.infoLogger.Printf("Skipping mutation for %s/%s due to policy check", pod.Namespace, pod.Name)
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	config, ok := whs.sidecarConfigs[mut]
	if !ok {
		whs.warningLogger.Printf("Skipping mutation for %s/%s due to missing configuration for mutation %s", pod.Namespace, pod.Name, mut)
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	// add the status injected annotation and create the patch
	annotations := map[string]string{admissionWebhookAnnotationStatusKey: "injected"}
	patchBytes, err := whs.createPatch(&pod, config, annotations)
	if err != nil {
		return &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	whs.infoLogger.Printf("AdmissionResponse: patch=%v\n", string(patchBytes))
	return &admissionv1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *admissionv1.PatchType {
			pt := admissionv1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

// Serve method for webhook server
func (whs *WebhookServer) Serve(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := io.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		whs.warningLogger.Println("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		whs.warningLogger.Printf("Content-Type=%s, expect application/json", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	// decode the admission request
	var admissionResponse *admissionv1.AdmissionResponse
	ar := admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
		whs.warningLogger.Printf("Can't decode body: %v", err)
		admissionResponse = &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		// mutate the pod passed in
		admissionResponse = whs.mutate(&ar)
	}

	// encode the admission response
	admissionReview := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
	}

	// set the response
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
		}
	}

	// encode the response
	resp, err := json.Marshal(admissionReview)
	if err != nil {
		whs.warningLogger.Printf("Can't encode response: %v", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}

	// write the response
	whs.infoLogger.Printf("Ready to write reponse ...")
	if _, err := w.Write(resp); err != nil {
		whs.warningLogger.Printf("Can't write response: %v", err)
		http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
	}
}
