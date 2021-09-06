Make possible to add custom command-line arguments to operator in the helm chart.

We need exactly `CAPABILITY_NAMED_IAM` command-line argument for operator, but in the current version of  helm chart in the original GitHub repository it is not possible to provide such argument from `values.yaml` file.

Our PR: https://github.com/linki/cloudformation-operator/pull/242