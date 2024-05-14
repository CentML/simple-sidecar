A simple tool for testing the simple side car config is valid.

```
go run tester.go <yourfile>.yaml
```

Your yaml file should contain configuration for formatting which would be nested under 'simpleSidecarConfig' in your values.yaml for the helm chart. 

Here's an example: 

```yaml
ubuntutest: 
  containers:
  - args:
    - -c
    - sleep infinity
    command:
    - /bin/sh
    image: ubuntu
    name: ubuntu
```

Here's the output:

```sh
INFO: 2024/05/14 14:26:54 webhook.go:98: New configuration: sha256sum 0c6dfafe714cfaba82156799afefece19db86fcf7d56d57b2af7baf228dc9df3
ubuntutest:
  Containers:
  - args:
    - -c
    - sleep infinity
    command:
    - /bin/sh
    image: ubuntu
    name: ubuntu
    resources: {}
  EnvVars: null
  InitContainers: null
  VolumeMounts: null
  Volumes: null
  ```

In this case there were no errors, and the outputed result looks correct. 