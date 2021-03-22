# cloudformation-operator

This is the Helm chart for the [cloudformation-operator](https://github.com/linki/cloudformation-operator)

## Prerequisites

- Kubernetes 1.17+

## Installing the chart
Create AWS resources with Kubernetes
The chart can be installed by running:

```bash
$ helm install cloudformation-operator-system helm/cloudformation-operator
```

## Configuration

The following table lists the configurable parameters of the cloudformation-operator chart and their default values.

| Parameter                 | Description                            | Default                                            |
| ------------------------- | -------------------------------------- | -------------------------------------------------- |
| `operator.region`         | The AWS_REGION to set                  | `eu-central-1`                                     |
| `image.repository`        | Container image repository             | `quay.io/linki/cloudformation-operator`            |
| `image.tag`               | Container image tag                    |                                                    |
| `image.sha`               | Container image shasum (overrides tag) |                                                    |
| `image.pullPolicy`        | Container pull policy                  | `IfNotPresent`                                     |
| `affinity`                | affinity settings for pod assignment   | `{}`                                               |
| `extraEnv`                | Optional environment variables         | `[]`                                               |
| `extraVolumes`            | Custom Volumes                         | `[]`                                               |
| `extraVolumeMounts`       | Custom VolumeMounts                    | `[]`                                               |
| `nodeSelector`            | Node labels for pod assignment         | `{}`                                               |
| `podAnnotations`          | Annotations to attach to pod           | `{}`                                               |
| `rbac.create`             | Create RBAC roles                      | `true`                                             |
| `serviceAccount.name`     | Existing ServiceAccount to use         | `cloudformation-operator`                          |                                          |
| `replicas`                | Deployment replicas                    | `1`                                                |
| `resources`               | container resource requests and limits | `{}`                                               |
| `tolerations`             | Toleration labels for pod assignment   | `[]`                                               |
| `tags`                    | You may want to assign tags to your CloudFormation stacks   | `[]`                          |
| `capability.enabled`      | Enable specified capabilities for all stacks managed by the operator instance   | `[]`      |