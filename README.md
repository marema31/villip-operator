# villip-operator
Kubernetes operator to deploy `villip` a simple http proxy that allow header/content replacement on the fly (see [villip github page](https://github.com/marema31/villip))

## Description

The operator will create two new k8s resources:

- villip.carmier.fr/v1/VillipRules: Configuration of the villip proxy
- villip.carmier.fr/v1/VillipProxy: Instance of villip proxy

More than one proxy can share the same villiprule and a proxy can use more than on villiprule. The operator relies
on labels to determine which villiprule will be used by a proxy instance.

### VillipRules
The spec of this ressource correspond to the [YAML configuration file format of villip](https://github.com/marema31/villip/tree/master?tab=readme-ov-file#yamljson-configuration-files) the only exception is the absence of the url that will be defined in the proxy ressource.

### VillipProxy
Configuration of one instance of villip proxy:

- size: number of replicas in the deployment
- ports: list of TCP port on which the instannce will be listening, configuration applied to each port differs and depends on the corresponding villiprules ressources
- ruleset: Label selectors used to determine the villiprules to be applied to this instance. Each entries in the list will be added to the list of villiprules (boolean OR operation). Each values in the same `matchlabels` entry must be present on a villiprule instance labels to select this ressource (boolean AND operation)
- target: Namespace, name of the k8s service, port and URI that will be proxified by this instance.

```yaml
apiVersion: "villip.carmier.fr/v1"
kind: "VillipProxy"
metadata:
  name: villip
  namespace: default
spec:
  size: 2
  ports:
    - 8080
    - 8081
    - 8082
  ruleset:
    - matchlabels:
        ruleset: villiptests
        basedon: port
    - matchlabels:
        ruleset: villiptests
        basedon: priority
  target:
    namespace: default
    name: smocker
    port: 8080
    uri: /
```

## Getting Started

### Deploy using the bundle with all YAML files

```sh
kubectl apply -f https://raw.githubusercontent.com/marema31/villip-operator/<tag or branch>/dist/install.yaml
```

### Deploy using Helm Chart


```sh
 helm install my-release https://raw.githubusercontent.com/marema31/villip-operator/<tag or branch>/dist/chart --create-namespace --namespace villip-operator-system
```

### Create the villiprules and villipproxy resources manifests
Detailled example are provided in examples/manifests folder. They are based on the [end-to-end villip test suite](https://github.com/marema31/villip/tree/master/e2e_tests).

## Contributing

This project is mainly a learning project for me, but it is used for development stack in several organizations.
If it fits your need but miss some features, feel free to fork it or propose pull request on it.

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
