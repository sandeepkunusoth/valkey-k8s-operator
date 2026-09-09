# Mutual TLS (mTLS) certificate-based ACL authentication

`networking.tls.authClients` and `networking.tls.authClientsUser` build on the [TLS Configuration](valkeycluster.md#tls) so that:

1. Clients can be required to present a TLS certificate (mTLS), and
2. Authenticated clients can be automatically logged in as a Valkey ACL user matching the certificate's Common Name (CN) or URI SAN.

> `authClientsUser: CN` requires Valkey >= 9.0; `authClientsUser: URI` requires Valkey >= 9.1.

## Valkey defaults vs operator defaults for mTLS

By default, Valkey uses mutual TLS and requires clients to present a valid certificate verified against trusted root CAs configured via `tls-ca-cert-file` or `tls-ca-cert-dir`. You may use `tls-auth-clients no` to disable client authentication.

Valkey requires client certificates on a TLS port by default. The operator does not: when `networking.tls.authClients` is unset, it renders `tls-auth-clients optional`, so a TLS client may connect without presenting a certificate. This keeps existing TLS clusters working and makes enabling mTLS an explicit opt-in.

`authClientsUser` defaults to `Disabled`, which leaves the directive out of `valkey.conf` entirely rather than rendering `off`. `tls-auth-clients-user` does not exist before Valkey 9.0, and its default is already `off`, so omitting it keeps older servers starting without changing behaviour.

## Quick start

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyCluster
metadata:
  name: valkeycluster-mtls
spec:
  shards: 3
  replicas: 1
  networking:
    tls:
      certificates:
        server:
          secretName: valkey-server-tls
      authClients: Required
      authClientsUser: CN
  users:
    - name: alice
      enabled: true
      resetpass: true
      permissions: "+@all ~app:* &events:*"
```

With `authClients: Required` + `authClientsUser: CN`, any TLS client whose certificate has `CN=alice` is automatically authenticated as the ACL user `alice` -- no `AUTH` command required. Pass `resetpass: true` with this configuration so authentication relies exclusively on the client certificate.

With `authClients: Required`, Valkey requires a valid client certificate at the TLS handshake, but that does not disable password-based ACL authentication. Clients can still authenticate with `AUTH` as long as they present a client certificate signed by the configured CA. Today operator user, health check probes, redis exporter all present the server certificate to satisfy this.

## Configuration

| Field | Values | Default | Description |
|---|---|---|---|
| `authClients` | `Required`, `Optional`, `Disabled` | `Optional` | Whether clients must present a certificate signed by the configured CA. |
| `authClientsUser` | `CN`, `URI`, `Disabled` | `Disabled` | Which certificate field selects the ACL user. |

`authClientsUser: CN` or `authClientsUser: URI` has no effect when `authClients: Disabled` (Valkey ignores client certificates entirely), so this combination is rejected at admission time.

### `authClients` values

`authClients` API values are mapped to Valkey `tls-auth-clients` directive values when the operator renders the config.

| `authClients` | Rendered | Meaning |
|---|---|---|
| `Optional` | `tls-auth-clients optional` | Default. Both authenticated and unauthenticated TLS clients are allowed. |
| `Required` | `tls-auth-clients yes` | Enforces mTLS -- clients without a valid client certificate are rejected at the TLS handshake. |
| `Disabled` | `tls-auth-clients no` | Client certificates are ignored entirely. |

| `authClientsUser` | Rendered |
|---|---|
| `CN` | `tls-auth-clients-user CN` |
| `URI` | `tls-auth-clients-user URI` |
| `Disabled` | *(directive omitted)* |

### Rendered Valkey configuration (valkey.conf)

```text
tls-auth-clients "yes"    # rendered from authClients: Required
tls-auth-clients-user CN/URI   # rendered from authClientsUser: CN or authClientsUser: URI
```

The rest of the rendered TLS block (`tls-port`, `tls-cluster yes`, `tls-replication yes`, etc.) is unchanged from the existing TLS feature documented in [valkeycluster.md](./valkeycluster.md#tls).

## Issuing certificates with cert-manager

Both server and client certificates must be signed by the **same CA** so the server can validate the client. The recommended pattern uses a self-signed bootstrap Issuer to mint a CA Certificate, and a CA Issuer (referencing that CA Secret) to sign the server and client leaves:

```yaml
apiVersion: cert-manager.io/v1
kind: Issuer
metadata: { name: custom-issuer }
spec: { selfSigned: {} }
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata: { name: valkey-ca }
spec:
  isCA: true
  commonName: valkey-ca
  secretName: valkey-ca
  issuerRef: { name: custom-issuer, kind: Issuer, group: cert-manager.io }
---
apiVersion: cert-manager.io/v1
kind: Issuer
metadata: { name: valkey-ca-issuer }
spec:
  ca: { secretName: valkey-ca }
---
# Server cert (referenced from spec.networking.tls.certificates.server.secretName)
apiVersion: cert-manager.io/v1
kind: Certificate
metadata: { name: valkey-server-tls }
spec:
  secretName: valkey-server-tls
  commonName: valkeycluster-mtls.default.svc.cluster.local
  dnsNames: [ valkeycluster-mtls.default.svc.cluster.local ]
  issuerRef: { name: valkey-ca-issuer, kind: Issuer, group: cert-manager.io }
---
# Client cert; CN=alice authenticates as the alice ACL user
apiVersion: cert-manager.io/v1
kind: Certificate
metadata: { name: valkey-client-alice }
spec:
  secretName: valkey-client-alice
  commonName: alice
  issuerRef: { name: valkey-ca-issuer, kind: Issuer, group: cert-manager.io }
```

## Connecting clients

```bash
valkey-cli \
  --tls \
  --cert client-tls.crt \
  --key client-tls.key \
  --cacert ca.crt \
  -h valkeycluster-mtls.default.svc.cluster.local \
  -p 6379 \
  PING
```

## Operator-managed connections

The operator, readiness and liveness probes, and metrics exporter all connect over the same TLS port and present the node's **server** certificate as their client certificate. That satisfies `authClients: Required`.

With `authClientsUser: CN`, the server certificate CN is the node FQDN, which does not name an ACL user. Valkey connects these clients as the unauthenticated default user and they then authenticate with `AUTH` as usual. Certificate mapping is therefore additive: it never has to succeed for operator-managed connections to work. You do not need to create an ACL user named after the server CN.

## Security considerations

### Do not use `nopass` on a certificate-mapped user

`nopass: true` lets any client run `AUTH <user> <any-password>` and succeed, whether or not it holds the matching client certificate. This is still true under `authClients: Required`: any client with a valid CA-signed certificate, regardless of its CN or URI, can then authenticate as any `nopass` user.

Set `resetpass: true` instead. That clears every password and disables `nopass`, so password authentication is impossible and the CN or URI from the client certificate becomes the only way to authenticate as that user.

```yaml
users:
  - name: alice
    enabled: true
    resetpass: true
```
