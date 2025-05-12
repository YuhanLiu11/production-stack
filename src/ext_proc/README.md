# Gateway Inference Extension

This directory contains the implementation of a gateway inference extension that provides load balancing and request routing capabilities for inference services. The extension is built using Envoy's External Processing (ext_proc) API and can be run both in Kubernetes and locally for development.

## Kubernetes Integration

To run in Kubernetes:

First, install the Gateway API CRDs:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.0.0/standard-install.yaml
```

Then, install the inference extension CRDs:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/download/v0.3.0/manifests.yaml
```

Finally, deploy the gateway extension pods:

```bash
kubectl apply -f operator/config/samples/gateway
```

## Architecture

The picker service implements the following flow:

1. Receives requests through Envoy's ext_proc API
2. For header requests:
   - Updates the list of available endpoints
   - Selects the next endpoint using round-robin
   - Sets the `x-inference-target` header
3. For body requests:
   - Forwards the request to the selected endpoint
   - Returns the response to the client
