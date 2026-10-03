# Run the server on Kubernetes

These manifests run midpoint-mcp-server in shared mode on Kubernetes. They are plain
YAML put together with Kustomize, which is built into kubectl. Read
[docs/misconfigurations.md](../../docs/misconfigurations.md) before you start. It
lists the settings that let one person act as another in midPoint.

`kustomization.yaml` lists `deployment.yaml` and `service.yaml`. The Deployment runs
one pod as user 65534, with a read-only root file system, no Linux capabilities, the
runtime's default seccomp profile and no Kubernetes API token. The Service makes the
server's port 3001 reachable inside the cluster. `ingress.example.yaml` is an example
of the TLS proxy that must sit in front of the server. It is not applied with the
rest, because it needs your host name and certificate.

## Deploy it

1. Copy [examples/keycloak.env](../../examples/keycloak.env) or
   [examples/entra.env](../../examples/entra.env) to a private file named
   `midpoint-mcp.env` outside the repository, and replace every placeholder in it.
   The examples leave out the service account's password on purpose, so add it as
   one more line:

   ```
   MIDPOINT_PASSWORD=<service-account-password>
   ```

   kubectl takes each value literally, so do not put quotes around it.

2. Create the Secret that holds every setting:

   ```sh
   kubectl create secret generic midpoint-mcp-server --from-env-file=midpoint-mcp.env
   ```

   Then delete your copy, or keep it only in your secret store.

3. Apply the manifests from the repository root:

   ```sh
   kubectl apply -k deploy/kubernetes
   ```

   Both commands act on the current namespace. Add `-n <namespace>` to both to
   choose another. `kubectl logs deploy/midpoint-mcp-server` shows a line that
   contains `resource-server mode (OIDC bearer)`. If the Secret lacks both the issuer
   and the audience, the server exits at startup instead, with `refusing to bind
   --http to non-loopback host "0.0.0.0"`. If it has only one of them, the server
   exits with `MIDPOINT_MCP_OIDC_ISSUER and MIDPOINT_MCP_OIDC_AUDIENCE must be set
   together`.

4. Put TLS in front of the server. Copy `ingress.example.yaml`, change the values its
   comments mark, and apply your copy with `kubectl apply -f`. Any other proxy that
   terminates TLS works too. The server speaks plain HTTP inside the cluster, so let
   only that proxy reach the Service, for example with a NetworkPolicy.

5. Check it:

   ```sh
   curl -i -X POST https://mcp.example.com/mcp
   ```

   It must answer `401` with the body `no bearer token`, because the request carries
   no token. Then connect your MCP client or gateway to `https://mcp.example.com/mcp`
   with a person's access token and call the `whoami` tool. It names that person and
   says `resource-server mode`.

To use the optional settings file, put it in a ConfigMap, mount it read-only into the
pod, and set `MIDPOINT_MCP_CONFIG` in the Secret to its path. These manifests do not
include that.

## Why one replica

The server keeps each MCP session in the memory of its pod. A request for a session
that another pod holds gets `404 session not found`, so a second replica works only
behind a proxy that sends every request of a session to the same pod, by hashing the
`Mcp-Session-Id` header. A restart or a rollout ends the open sessions, and MCP
clients then have to start new ones. The pod keeps no other state and needs no
volume.

## Why the probes check the TCP port

The server has no health endpoint, and it does not publish OAuth protected-resource
metadata. `/mcp` answers `401` without a token and every other path answers `404`, so
an HTTP probe would always fail. The port opens only after the settings have passed
validation and the identity provider's discovery document has been read, so a pod is
ready once the port accepts connections. The server reaches midPoint at startup only
when the settings file names request form items. The probes do not check midPoint.

## Pin a version

`kustomization.yaml` uses the image tag `latest`. Each release attaches a rendered
manifest with the image tag set to that release's version, so applying that file
installs exactly that version. To pin a version with these files instead, set
`newTag` in `kustomization.yaml` to the version you want.
