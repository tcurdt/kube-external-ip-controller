## Installation

Apply the manifests to your cluster:

```bash
kubectl apply -f https://raw.githubusercontent.com/tcurdt/kube-external-ip-controller/main/manifests.yaml
```

## Usage

### Annotating Services

To make a service use the node's IP as an external IP, add the `external-ip-interface` annotation to your service:

```yaml
apiVersion: v1
kind: Service
metadata:
  annotations:
    external-ip-interface: "eth0"
```

### DaemonSet

The DaemonSet will automatically deploy to all nodes in the cluster:

```bash
kubectl get daemonset -n kube-system external-ip-controller
```

### IP Controller Logs

```bash
kubectl logs -n kube-system -l app=external-ip-controller
```
