# Simple Sidecar (Injector)

This repo was originally forked from [morvencao's kube-sidecar-injector](https://github.com/morvencao/kube-sidecar-injector). It's been productionized and turned into a simple generic Kubernetes sidecar injector (using a [Kubernetes MutatingAdmissionWebhook](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#mutatingadmissionwebhook)).


## Quick Start 

You will need a certificate for the [mutating admission webhook](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#mutatingadmissionwebhook) that powers simple-sidecar. It is recommended that you use [Certificate Manager's CA Injector functionality](https://cert-manager.io/docs/concepts/ca-injector/) for a serious deployment as certificates inevitably expire and need to be replaced.  For this quickstart we're just going to use openssl cli. 


Some configuration we'll use:

```sh
NAMESPACE=simple-sidecar # helm chart default value
TLS_SECRET_NAME=simple-sidecar-tls #helm chart default value
SERVICE_NAME=simple-sidecar # the helm chart default name
```

Create the namespace in kubectl if it doesn't already exist:

```sh
kubectl create ns $NAMESPACE
```

Generate the certificates:

```sh
cd examples
./certs.sh $NAMESPACE $TLS_SECRET_NAME $SERVICE_NAME
```

Create the secrets using the command printed:
```sh
kubectl create secret generic simple-sidecar --from-file=tls.crt=server.crt --from-file=tls.key=server.key --from-file=ca.crt=ca.crt --namespace simple-sidecar
```

Create a values.yaml for the helm installation, use the caBundle provided by the cert.sh script:

```yaml
caBundle: <YOUR BUNDLE HERE>

simpleSidecarConfig:
  ubuntu: 
    containers:
    - args:
      - -c
      - sleep infinity
      command:
      - /bin/sh
      image: ubuntu
      name: ubuntu
```


Install the helm chart:

```sh
helm repo add simple-sidecar https://centml.github.io/simple-sidecar
helm repo update
helm install simple-sidecar simple-sidecar/simple-sidecar -f values.yaml
```

Let's injected the ubuntu container into another container. First we need to create (or update) a namespace with the sidecar-injection label set to true:

```sh
kubectl apply -f - << EOF
apiVersion: v1
kind: Namespace
metadata:
  labels:
    simple-sidecar.centml.ai/injection: enabled
  name: injectable
EOF
```

Now let's create a pod with the `simple-sidecar.centml.ai/inject` annotation pointing to the ubuntu pod we've configured:


```sh
kubectl apply -f - << EOF
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: injectable
  annotations:
    simple-sidecar.centml.ai/inject: "ubuntu"
spec:
  containers:
  - name: curl-container
    image: curlimages/curl
    command: ["/bin/sleep"]
    args: ["infinity"]
EOF
```

You should now have a ubuntu pod injected in your curl pod: 

```sh
kubectl get pod my-pod -n injectable -o jsonpath='{.spec.containers}' | jq
```

```json
[
   {
      "args":[
         "infinity"
      ],
      "command":[
         "/bin/sleep"
      ],
      "image":"curlimages/curl",
      "imagePullPolicy":"Always",
      "name":"curl-container",
    ...
   {
      "args":[
         "-c",
         "sleep infinity"
      ],
      "command":[
         "/bin/sh"
      ],
      "image":"ubuntu",
      "imagePullPolicy":"Always",
      "name":"ubuntu",
   ...
   }
]
```

## Configuring side cars

The easiest way to learn what can be configured is to look at the Config struct in pkg/webhook/webhook.go

```sh
go doc -all pkg/webhook/webhook.go
```

Look for:
```txt
type Config struct {

	// InitContainers - inject one or more initContainers into the pod spec.
	InitContainers []corev1.Container

	// Containers - inject one or more containers into the pod spec.
	Containers []corev1.Container

	// ExistingContainerConfig - configuration for injecting into the pre-existing containers.
	ExistingContainerConfig
}
    Config is the struct used to parse injection config items for Simple
    Sidecar. The InitContainers, Containers, Volumes, and EnvVars fields are
    arrays of Kubernetes objects that will be added to the pod spec.

type ExistingContainerConfig struct {
	// Volumes - inject one or more volumes into pre-existing pod specs.
	Volumes []corev1.Volume

	// EnvVars - inject one or more environment variables into pre-existing container specs.
	EnvVars []corev1.EnvVar

	// VolumeMounts - inject one or more volume mounts into pre-existing container specs.
	// BEFORE sidecar injection.
	VolumeMounts []corev1.VolumeMount
}
    ExistingContainerConfig provides configuration for injecting into the
    pre-existing containers. This is useful for utilizing the functionality of
    injected containers

```

The fields of these Config structs reference the kubernetes go source itself. So the syntax perfectly matches if you were defining a container etc by hand using yaml. You can check out the source [here](https://github.com/kubernetes/api/blob/master/core/v1/types.go). 


### Basic Config


Define a config type in your values.yaml:

```yaml
mytype:
  containers:
  - args:
      - -c
      - sleep infinity
      command:
      - /bin/sh
      image: ubuntu
      name: ubuntu  
```

In order to use the 'mytype' injection:

    1) Create or update a namespace with the 'sidecar-injection: enabled' *label*.
    2) Create a pod in this namespace with the 'simple-sidecar.centml.ai: mytype' annotation. 

That's it. Define as many configurations as you like with either containers or initContainers 

### Advanced Config

You can also inject things like:
  1) volumes into the existing pods
  2) environment variables, volume mounts into the pre-existing containers

This let's you leverage functionality that might be provided by your injected containers. 

## Using cert-manager's CA Injector

Follow the documentation related to [installing cert-manager](https://cert-manager.io/docs/) and then using it's [CA Injector functionality](https://cert-manager.io/docs/concepts/ca-injector/. 

Setup a [self signed certificate](https://cert-manager.io/docs/configuration/selfsigned/) which will auto update to the secret location you've configured simple-sidecar to use. 

