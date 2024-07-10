package webhook

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// compareConfigs compares two MultiConfig objects and returns true if they are equal
// and false otherwise. Helper function for testing.
func compareConfigs(a, b MultiConfig) bool {
	aData, err := yaml.Marshal(a)
	if err != nil {
		return false
	}
	bData, err := yaml.Marshal(b)
	if err != nil {
		return false
	}
	return string(aData) == string(bData)
}

// tempFileSetup creates a temporary file with the given data and returns the file path
func tempFileSetup(t *testing.T, data string) string {
	// Create a temporary file to hold the test YAML configuration
	tempFile, err := os.CreateTemp("", "test-config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temporary file: %v", err)
	}

	// Write the test YAML configuration to the temporary file
	if _, err := tempFile.Write([]byte(data)); err != nil {
		t.Fatalf("Failed to write to temporary file: %v", err)
	}
	tempFile.Close()

	return tempFile.Name()
}

// Sample Good configuration for testing
const goodConfig = `
test-config:
  InitContainers:
    - name: init-container
      image: init-image
  Containers:
    - name: main-container
      image: main-image
  Volumes:
    - name: volume
      emptyDir: {}
  EnvVars:
    - name: ENV_VAR
      value: env_value
  VolumeMounts:
    - name: volume-mount
      mountPath: /mnt
`

func TestLoadConfigSuccess(t *testing.T) {

	tfn := tempFileSetup(t, goodConfig)
	defer os.Remove(tfn)

	// Load the configuration from the temporary file
	config, err := LoadConfig(tfn)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Define the expected configuration
	expectedConfig := MultiConfig{
		"test-config": Config{
			InitContainers: []corev1.Container{
				{
					Name:  "init-container",
					Image: "init-image",
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "main-container",
					Image: "main-image",
				},
			},
			ExistingContainerConfig: ExistingContainerConfig{
				Volumes: []corev1.Volume{
					{
						Name: "volume",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
				EnvVars: []corev1.EnvVar{
					{
						Name:  "ENV_VAR",
						Value: "env_value",
					},
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "volume-mount",
						MountPath: "/mnt",
					},
				},
			},
		},
	}

	// Compare the loaded configuration with the expected configuration
	if !compareConfigs(config, expectedConfig) {
		t.Errorf("Loaded configuration does not match expected configuration.\nGot: %+v\nExpected: %+v", config, expectedConfig)
	}
}

// Sample empty configuration for testing
const emptyConfig = `
test-config:
`

// TestLoadConfigEmpty tests loading an empty configuration file.
// The configuration file is empty, so the expected configuration is an empty Config object
// but there are no errors.
func TestLoadConfigEmpty(t *testing.T) {
	tfn := tempFileSetup(t, emptyConfig)
	defer os.Remove(tfn)

	// Load the configuration from the temporary file
	config, err := LoadConfig(tfn)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Define the expected configuration
	expectedConfig := MultiConfig{
		"test-config": Config{},
	}

	// Compare the loaded configuration with the expected configuration
	if !compareConfigs(config, expectedConfig) {
		t.Errorf("Loaded configuration does not match expected configuration.\nGot: %+v\nExpected: %+v", config, expectedConfig)
	}
}

// Sample bad configuration for testing
const badConfig = `
test-config:
  InitContainers:
    - name: init-container
      imagetypo: init-image
`

func TestBadConfig(t *testing.T) {
	tfn := tempFileSetup(t, badConfig)
	defer os.Remove(tfn)
	// Load the configuration from the temporary file
	_, err := LoadConfig(tfn)
	assert.True(t, err == nil, "expected error")
}

// Test cases for mutationRequired function
func TestMutationRequired(t *testing.T) {
	tests := []struct {
		name        string
		ignoredList []string
		metadata    *metav1.ObjectMeta
		expectedReq bool
		expectedMut string
	}{
		{
			name:        "Namespace is ignored (ignoreList respected)",
			ignoredList: []string{"kube-system"},
			metadata: &metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "kube-system",
			},
			expectedReq: false,
			expectedMut: "",
		},
		{
			name:        "Annotation is missing, no mutation required",
			ignoredList: []string{"kube-system"},
			metadata: &metav1.ObjectMeta{
				Name:        "test-pod",
				Namespace:   "default",
				Annotations: map[string]string{},
			},
			expectedReq: false,
			expectedMut: "",
		},
		{
			name:        "Mutation already injected, even if injection annotation is set",
			ignoredList: []string{"kube-system"},
			metadata: &metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				Annotations: map[string]string{
					admissionWebhookAnnotationInjectKey: "true",
					admissionWebhookAnnotationStatusKey: "injected",
				},
			},
			expectedReq: false,
			expectedMut: "",
		},
		{
			name:        "Mutation required",
			ignoredList: []string{"kube-system"},
			metadata: &metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				Annotations: map[string]string{
					admissionWebhookAnnotationInjectKey: "true",
				},
			},
			expectedReq: true,
			expectedMut: "true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			whs := &WebhookServer{
				infoLogger: log.New(io.Discard, "", 0),
			}
			req, mut := whs.mutationRequired(tt.ignoredList, tt.metadata)
			if req != tt.expectedReq {
				t.Errorf("%s: expected required to be %v, got %v", tt.name, tt.expectedReq, req)
			}
			if mut != tt.expectedMut {
				t.Errorf("%s: expected mutation to be %v, got %v", tt.name, tt.expectedMut, mut)
			}
		})
	}
}

// TestAddContainer_WithExistingContainer tests the addContainer function
func TestAddContainer_WithExistingContainer(t *testing.T) {
	whs := &WebhookServer{}

	// there will be at least one container in the target slice
	target := []corev1.Container{
		{
			Name:  "existing-container",
			Image: "existing-image",
		},
	}

	adding := []corev1.Container{
		{
			Name:  "new-container",
			Image: "new-image",
		},
	}

	patch := whs.addContainer(target, adding, "/spec/containers")

	assert.Equal(t, 1, len(patch), "Expected 1 patch operation")
	assert.Equal(t, "add", patch[0].Op, "Expected 'add' operation")
	assert.Equal(t, "/spec/containers/-", patch[0].Path, "Expected '/spec/containers/-' path")

	addedContainer, ok := patch[0].Value.(corev1.Container)
	assert.True(t, ok, "Expected corev1.Container value")
	assert.Equal(t, "new-container", addedContainer.Name, "Expected 'new-container' name")
	assert.Equal(t, "new-image", addedContainer.Image, "Expected 'new-image'")
}

// TestAddContainer_EmptyAdditions tests the addContainer function when
// the target slice is empty.
func TestAddContainer_EmptyAdditions(t *testing.T) {
	whs := &WebhookServer{}

	target := []corev1.Container{
		{
			Name:  "existing-container",
			Image: "existing-image",
		},
	}

	adding := []corev1.Container{}
	patch := whs.addContainer(target, adding, "/spec/containers")
	assert.Equal(t, 0, len(patch), "Expected 1 patch operation")
}

// TestAddContainer_EmptyAdditions tests the addContainer function when
// the target slice is empty.
func TestAddContainer_EmptyTarget(t *testing.T) {
	whs := &WebhookServer{}

	target := []corev1.Container{}

	adding := []corev1.Container{
		{
			Name:  "new-container",
			Image: "new-image",
		},
	}

	// Call addContainer, expect two patch operations
	patch := whs.addContainer(target, adding, "/spec/containers")
	assert.Equal(t, 2, len(patch), "Expected 2 patch operations")

	// Check the first patch operation (initialization)
	assert.Equal(t, "add", patch[0].Op, "Expected 'add' operation")
	assert.Equal(t, "/spec/containers", patch[0].Path, "Expected '/spec/containers' path")
	cons, ok := patch[0].Value.([]corev1.Container)
	assert.True(t, ok, "Expected []corev1.Container value")
	assert.Equal(t, 0, len(cons), "Expected 0 containers in initial value")

	// Check the second patch operation (adding the new container)
	assert.Equal(t, "add", patch[1].Op, "Expected 'add' operation")
	assert.Equal(t, "/spec/containers/-", patch[1].Path, "Expected '/spec/containers/-' path")
	addedContainer, ok := patch[1].Value.(corev1.Container)
	assert.True(t, ok, "Expected corev1.Container value")
	assert.Equal(t, "new-container", addedContainer.Name, "Expected 'new-container' name")
	assert.Equal(t, "new-image", addedContainer.Image, "Expected 'new-image'")

}

// TestAddVolumeNoVolumes tests the addVolume method of the WebhookServer struct.
// It verifies that the method correctly handles the case where no volumes are
// currently present in the target pod.
func TestAddVolumeNoVolumes(t *testing.T) {
	whs := &WebhookServer{}

	target := []corev1.Volume{}

	added := []corev1.Volume{
		{
			Name: "new-volume",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	patch := whs.addVolume(target, added, "/spec/volumes")
	assert.Equal(t, 2, len(patch), "Expected 2 patch operations")

	// Check the first patch operation (initialization)
	assert.Equal(t, "add", patch[0].Op, "Expected 'add' operation for initialization")
	assert.Equal(t, "/spec/volumes", patch[0].Path, "Expected '/spec/volumes' path for initialization")
	initVolume, ok := patch[0].Value.([]corev1.Volume)
	assert.True(t, ok, "Expected []corev1.Volume value for initialization")
	assert.Equal(t, 0, len(initVolume), "Expected 0 volumes in initial value")

	// Check the second patch operation (adding the new volume)
	assert.Equal(t, "add", patch[1].Op, "Expected 'add' operation for adding new volume")
	assert.Equal(t, "/spec/volumes/-", patch[1].Path, "Expected '/spec/volumes/-' path for adding new volume")
	addedVolume, ok := patch[1].Value.(corev1.Volume)
	assert.True(t, ok, "Expected corev1.Volume value")
	assert.Equal(t, "new-volume", addedVolume.Name, "Expected 'new-volume' name")
}

// TestAddVolumeWithExistingVolumes tests the addVolume method of the WebhookServer struct.
// It verifies that the method correctly handles the case where volumes are already present in the target pod.
func TestAddVolumeWithExistingVolumes(t *testing.T) {
	whs := &WebhookServer{}

	target := []corev1.Volume{
		{
			Name: "existing-volume",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	added := []corev1.Volume{
		{
			Name: "new-volume",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	// Call addVolume, expect one patch operation
	patch := whs.addVolume(target, added, "/spec/volumes")
	assert.Equal(t, 1, len(patch), "Expected 1 patch operation")

	// Check the patch operation
	assert.Equal(t, "add", patch[0].Op, "Expected 'add' operation")
	assert.Equal(t, "/spec/volumes/-", patch[0].Path, "Expected '/spec/volumes/-' path")
	addedVolume, ok := patch[0].Value.(corev1.Volume)
	assert.True(t, ok, "Expected corev1.Volume value")
	assert.Equal(t, "new-volume", addedVolume.Name, "Expected 'new-volume' name")
}

// TestUpdateAnnotationsWithSingleExistingAnnotations tests the updateAnnotation method of the WebhookServer struct.
// It verifies that the method correctly handles the case where annotations are already present in the target pod.
func TestUpdateAnnotationsWithSingleExistingAnnotations(t *testing.T) {
	whs := &WebhookServer{}

	target := map[string]string{
		"existing-annotation": "existing-value",
	}

	added := map[string]string{
		"existing-annotation": "new-value",
	}

	patch := whs.updateAnnotations(target, added)

	assert.Equal(t, 1, len(patch), "Expected 1 patch operation")
	assert.Equal(t, "replace", patch[0].Op, "Expected 'replace' operation")
	assert.Equal(t, "/metadata/annotations/existing-annotation", patch[0].Path, "Expected '/metadata/annotations/existing-annotation' path")

	newValue, ok := patch[0].Value.(string)
	assert.True(t, ok, "Expected string value")
	assert.Equal(t, "new-value", newValue, "Expected 'new-value'")
}

// TestUpdateAnnotationsWithNilTarget tests the updateAnnotation method of the WebhookServer struct.
// It verifies that the method correctly handles the case where the target annotations map is nil.
func TestUpdateAnnotationsWithNilTarget(t *testing.T) {
	whs := &WebhookServer{}

	var target map[string]string = nil

	added := map[string]string{
		"new-annotation": "new-value",
	}

	patch := whs.updateAnnotations(target, added)

	assert.Equal(t, 2, len(patch), "Expected 2 patch operations")
	assert.Equal(t, "add", patch[0].Op, "Expected 'add' operation")
	assert.Equal(t, "/metadata/annotations", patch[0].Path, "Expected '/metadata/annotations' path")

	annotationsMap, ok := patch[0].Value.(map[string]string)
	assert.True(t, ok, "Expected map[string]string value")
	assert.Equal(t, 0, len(annotationsMap), "Expected empty map")

	assert.Equal(t, "add", patch[1].Op, "Expected 'add' operation")
	assert.Equal(t, "/metadata/annotations/new-annotation", patch[1].Path, "Expected '/metadata/annotations/new-annotation' path")

	newValue, ok := patch[1].Value.(string)
	assert.True(t, ok, "Expected string value")
	assert.Equal(t, "new-value", newValue, "Expected 'new-value'")
}

// TestUpdateAnnotationsWithNonNilTargetAndExistingAnnotations tests the updateAnnotation method of the WebhookServer struct.
// It verifies that the method correctly handles the case where annotations are already present in the target pod.
func TestUpdateAnnotationsWithNonNilTargetAndExistingAnnotations(t *testing.T) {
	whs := &WebhookServer{}

	target := map[string]string{
		"existing-annotation": "existing-value",
		"another-annotation":  "another-value",
	}

	added := map[string]string{
		"existing-annotation": "new-value",
		"new-annotation":      "new-value",
	}

	patch := whs.updateAnnotations(target, added)

	assert.Equal(t, 2, len(patch), "Expected 2 patch operations")

	expectedPatches := map[string]patchOperation{
		"/metadata/annotations/existing-annotation": {
			Op:    "replace",
			Path:  "/metadata/annotations/existing-annotation",
			Value: "new-value",
		},
		"/metadata/annotations/new-annotation": {
			Op:    "add",
			Path:  "/metadata/annotations/new-annotation",
			Value: "new-value",
		},
	}

	for _, p := range patch {
		expectedPatch, exists := expectedPatches[p.Path]
		assert.True(t, exists, "Unexpected patch path: %s", p.Path)
		assert.Equal(t, expectedPatch.Op, p.Op, "Unexpected operation for path: %s", p.Path)
		assert.Equal(t, expectedPatch.Value, p.Value, "Unexpected value for path: %s", p.Path)
	}
}

// Test case: Verify addVolumeMounts adds volume mounts to containers in a pod,
// where some containers already have volume mounts and some do not.
// Ensures 'volumeMounts' fields are created for containers without them
// and volume mounts are added to each container.
func TestAddVolumeMounts_MixedExistingVolumeMounts_MultipleContainers(t *testing.T) {
	// Initialize the WebhookServer with a logger (using a nil logger for simplicity)
	whs := &WebhookServer{
		infoLogger: log.New(io.Discard, "", 0),
	}

	// Create a pod with multiple containers, some with existing volume mounts, some without
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "container1",
					VolumeMounts: []corev1.VolumeMount{
						{Name: "existing-mount", MountPath: "/mnt/existing"},
					},
				},
				{Name: "container2"},
			},
		},
	}

	// Define the volume mounts to add
	volumeMounts := []corev1.VolumeMount{
		{Name: "new-mount-1", MountPath: "/mnt/new1"},
		{Name: "new-mount-2", MountPath: "/mnt/new2"},
	}

	// Call addVolumeMounts function
	patch := whs.addVolumeMounts(pod, volumeMounts)

	// Expected patch operations
	expectedPatch := []patchOperation{
		{Op: "add", Path: "/spec/containers/0/volumeMounts/-", Value: corev1.VolumeMount{Name: "new-mount-1", MountPath: "/mnt/new1"}},
		{Op: "add", Path: "/spec/containers/0/volumeMounts/-", Value: corev1.VolumeMount{Name: "new-mount-2", MountPath: "/mnt/new2"}},
		{Op: "add", Path: "/spec/containers/1/volumeMounts", Value: []corev1.VolumeMount{}},
		{Op: "add", Path: "/spec/containers/1/volumeMounts/-", Value: corev1.VolumeMount{Name: "new-mount-1", MountPath: "/mnt/new1"}},
		{Op: "add", Path: "/spec/containers/1/volumeMounts/-", Value: corev1.VolumeMount{Name: "new-mount-2", MountPath: "/mnt/new2"}},
	}

	// Assert the patch operations match the expected patch operations
	assert.Equal(t, expectedPatch, patch, "patch operations do not match the expected operations")
}

// TestAddEnvVars_MixedExistingEnvVars_MultipleContainers Verifies addEnvVars adds env vars to multiple
// containers, some with existing env vars, some without.
// Ensures 'env' fields are created for containers without them and env vars are added to each container.
func TestAddEnvVars_MixedExistingEnvVars_MultipleContainers(t *testing.T) {
	// Initialize the WebhookServer with a logger (using a nil logger for simplicity)
	whs := &WebhookServer{
		infoLogger: log.New(io.Discard, "", 0),
	}

	// Create a pod with multiple containers, some with existing environment variables, some without
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "container1",
					Env: []corev1.EnvVar{
						{Name: "EXISTING_VAR", Value: "existing_value"},
					},
				},
				{Name: "container2"},
			},
		},
	}

	// Define the environment variables to add
	envVars := []corev1.EnvVar{
		{Name: "ENV_VAR_1", Value: "value1"},
		{Name: "ENV_VAR_2", Value: "value2"},
	}

	// Call addEnvVars function
	patch := whs.addEnvVars(pod, envVars)

	// Expected patch operations
	expectedPatch := []patchOperation{
		{Op: "add", Path: "/spec/containers/0/env/-", Value: corev1.EnvVar{Name: "ENV_VAR_1", Value: "value1"}},
		{Op: "add", Path: "/spec/containers/0/env/-", Value: corev1.EnvVar{Name: "ENV_VAR_2", Value: "value2"}},
		{Op: "add", Path: "/spec/containers/1/env", Value: []corev1.EnvVar{}},
		{Op: "add", Path: "/spec/containers/1/env/-", Value: corev1.EnvVar{Name: "ENV_VAR_1", Value: "value1"}},
		{Op: "add", Path: "/spec/containers/1/env/-", Value: corev1.EnvVar{Name: "ENV_VAR_2", Value: "value2"}},
	}

	// Assert the patch operations match the expected patch operations
	assert.Equal(t, expectedPatch, patch, "patch operations do not match the expected operations")
}

func TestCreatePatch(t *testing.T) {
	whs := &WebhookServer{
		infoLogger: log.New(ioutil.Discard, "", 0),
	}

	// Mock Pod and Config
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main-container",
					Image: "main-image",
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "volume",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}

	sidecarConfig := Config{
		InitContainers: []corev1.Container{

			{
				Name:  "sidecar-init-container",
				Image: "sidecar-init-image",
			},
		},
		Containers: []corev1.Container{
			{
				Name:  "sidecar-container",
				Image: "sidecar-image",
			},
		},
		ExistingContainerConfig: ExistingContainerConfig{
			Volumes: []corev1.Volume{
				{
					Name: "sidecar-volume",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			EnvVars: []corev1.EnvVar{
				{
					Name:  "SIDECAR_ENV_VAR",
					Value: "sidecar_env_value",
				},
			},

			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "sidecar-volume-mount",
					MountPath: "/mnt/sidecar",
				},
			},
		},
	}

	annotations := map[string]string{
		"sidecar-injected": "true",
	}

	// Expected patch operations, we Marshal this to []byte so that we can compare it with
	// the actual patch otherwise you get slight differences that don't actually matter
	// (nils instead of omissions etc)
	exp, err := json.Marshal([]patchOperation{
		{Op: "add", Path: "/spec/containers/0/volumeMounts", Value: []corev1.VolumeMount{}},
		{Op: "add", Path: "/spec/containers/0/volumeMounts/-", Value: corev1.VolumeMount{Name: "sidecar-volume-mount", MountPath: "/mnt/sidecar"}},

		{Op: "add", Path: "/spec/containers/0/env", Value: []corev1.EnvVar{}},
		{Op: "add", Path: "/spec/containers/0/env/-", Value: corev1.EnvVar{Name: "SIDECAR_ENV_VAR", Value: "sidecar_env_value"}},

		{Op: "add", Path: "/spec/initContainers", Value: []corev1.Container{}},
		{Op: "add", Path: "/spec/initContainers/-", Value: corev1.Container{Name: "sidecar-init-container", Image: "sidecar-init-image"}},

		{Op: "add", Path: "/spec/containers/-", Value: corev1.Container{Name: "sidecar-container", Image: "sidecar-image"}},
		{Op: "add", Path: "/spec/volumes/-", Value: corev1.Volume{Name: "sidecar-volume", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
		{Op: "add", Path: "/metadata/annotations", Value: map[string]string{}},
		{Op: "add", Path: "/metadata/annotations/sidecar-injected", Value: "true"},
	})
	if err != nil {
		t.Fatalf("Failed to marshal expected patch: %v", err)
	}

	// Call createPatch
	patch, err := whs.createPatch(pod, sidecarConfig, annotations)
	if err != nil {
		t.Fatalf("createPatch failed: %v", err)
	}

	// Unmarshal the patch to compare
	var ops []patchOperation
	if err := json.Unmarshal(patch, &ops); err != nil {
		t.Fatalf("Failed to unmarshal patch: %v", err)
	}

	var eops []patchOperation
	if err := json.Unmarshal(exp, &eops); err != nil {
		t.Fatalf("Failed to unmarshal expected patch: %v", err)
	}

	// Compare the generated patch with the expected patch
	if !comparePatches(ops, eops) {
		t.Errorf("Generated patch does not match expected patch.\nGot: %+v\nExpected: %+v", ops, eops)
	}
}

func comparePatches(a, b []patchOperation) bool {

	aData, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bData, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(aData) == string(bData)
}

// newTestWebhookServer creates a new WebhookServer with a test configuration
func newTestWebhookServer() *WebhookServer {
	return &WebhookServer{
		sidecarConfigs: MultiConfig{
			"test-config": Config{
				InitContainers: []corev1.Container{
					{
						Name:  "init-container",
						Image: "init-image",
					},
				},
				Containers: []corev1.Container{
					{
						Name:  "sidecar-container",
						Image: "sidecar-image",
					},
				},
				ExistingContainerConfig: ExistingContainerConfig{
					Volumes: []corev1.Volume{
						{
							Name: "test-volume",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
					EnvVars: []corev1.EnvVar{
						{
							Name:  "TEST_ENV",
							Value: "test_value",
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "test-volume",
							MountPath: "/test/path",
						},
					},
				},
			},
		},
		infoLogger:    log.New(io.Discard, "", 0),
		warningLogger: log.New(io.Discard, "", 0),
		errorLogger:   log.New(io.Discard, "", 0),
	}

}

// TestMutate_UnmarshalError tests the mutate function when the request object
// cannot be unmarshalled
func TestMutate_UnmarshalError(t *testing.T) {
	whs := newTestWebhookServer()

	ar := &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			Object: runtime.RawExtension{
				Raw: []byte("invalid json"),
			},
		},
	}

	resp := whs.mutate(ar)
	assert.Equal(t, "invalid character 'i' looking for beginning of value", resp.Result.Message)
}

// TestMutate_SkippingMutation tests the mutate function when the mutation is skipped
// because the pod has already been injected
func TestMutate_SkippingMutation(t *testing.T) {
	whs := newTestWebhookServer()

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			Annotations: map[string]string{
				admissionWebhookAnnotationStatusKey: "injected",
			},
		},
	}
	podBytes, _ := json.Marshal(pod)

	ar := &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			Namespace: "default",
			Name:      "test-pod",
			Object: runtime.RawExtension{
				Raw: podBytes,
			},
		},
	}

	resp := whs.mutate(ar)

	assert.True(t, resp.Allowed, "Expected request to be allowed")
	assert.Nil(t, resp.Patch, "Expected no patch")
}

// TestMutate_Success tests the mutate function when the mutation is successful
func TestMutate_Success(t *testing.T) {
	whs := newTestWebhookServer()

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			Annotations: map[string]string{
				admissionWebhookAnnotationInjectKey: "test-config",
			},
		},
	}
	podBytes, _ := json.Marshal(pod)

	ar := &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			Namespace: "default",
			Name:      "test-pod",
			Object: runtime.RawExtension{
				Raw: podBytes,
			},
		},
	}

	resp := whs.mutate(ar)
	assert.True(t, resp.Allowed, "Expected request to be allowed")
	assert.NotNil(t, resp.Patch, "Expected patch")
	assert.Equal(t, admissionv1.PatchTypeJSONPatch, *resp.PatchType, "Expected JSON patch type")
}

// TestMutate_PolicyCheck tests the mutate function when the pod is in a restricted namespace
func TestMutate_PolicyCheck(t *testing.T) {
	whs := newTestWebhookServer()

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			// kube-system is a no-no
			Namespace: metav1.NamespaceSystem,
			Name:      "test-pod",
			Annotations: map[string]string{
				admissionWebhookAnnotationInjectKey: "test-config",
			},
		},
	}
	podBytes, _ := json.Marshal(pod)

	ar := &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			Namespace: metav1.NamespaceSystem,
			Name:      "test-pod",
			Object: runtime.RawExtension{
				Raw: podBytes,
			},
		},
	}

	resp := whs.mutate(ar)
	assert.True(t, resp.Allowed, "Expected request to be allowed")
	assert.Nil(t, resp.Patch, "Expected no patch")
}

// TestMutate_NoAnnotations tests the mutate function when the pod has no annotations
func TestMutate_NoAnnotations(t *testing.T) {
	whs := newTestWebhookServer()

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
	}
	podBytes, _ := json.Marshal(pod)

	ar := &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			Namespace: "default",
			Name:      "test-pod",
			Object: runtime.RawExtension{
				Raw: podBytes,
			},
		},
	}

	resp := whs.mutate(ar)
	assert.True(t, resp.Allowed, "Expected request to be allowed")
	assert.Nil(t, resp.Patch, "Expected no patch")
}
