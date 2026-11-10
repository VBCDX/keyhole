Interop vectors. They're public certificates only; no private key is kept.

- `go-service.csr`: made by `keyhole service csr` (this repo, Go).
- `rust-service.pem`: what vbcdx-control's CA (`Ca::sign_service_csr`) signed from that CSR, for `keyhole` at `127.0.0.1`.
- `rust-control.pem`: that CA's control client certificate (`Ca::issue_control_client`).
- `rust-ca.pem`: the CA certificate.

The service certificate is valid for 30 days from 2026-11-05. The tests verify at a fixed time inside that window.
