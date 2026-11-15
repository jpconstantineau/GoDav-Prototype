# Deterministic synthetic loopback fixtures

This test-only descendant of e68f378fcae9b6228bfbc7769a5daa004deba1f8
records the IPv4 listener address selected by newServerFixture. The shared
fixture client accepts only the exact localhost authority for that endpoint
and dials its numeric 127.0.0.1 address over tcp4. Unexpected authorities,
networks, ports or inconsistent fixture metadata fail before dialing. There
is no proxy, redirect following or fallback to ambient hostname resolution.

Request URLs, HTTP Host, Destination headers and the public origin remain
https://localhost:<port>. TLS still sends the localhost name, verifies the
localhost certificate against the disposable fixture roots and requires the
same minimum version. Authentication and application assertions are unchanged.

The two hostname-based raw TLS calls in files_windows_test.go use the same
validated endpoint, retaining their supplied roots, localhost ServerName and
TLS minimum. Existing numeric loopback raw TLS calls already avoid hostname
resolution and remain unchanged. No production code or system settings change.

These fixtures exercise protocol, publication and lifecycle behavior, not the
machine's resolver. Host/origin rejection, certificate-name validation and
verified TLS remain separate existing assertions. OS localhost resolution,
IPv4/IPv6 selection and native-client connectivity remain separate acceptance
work. This fixture change does not explain or clear the retained sanitizer
abort from the original candidate; exact independent review and any later
aggregate race evidence must be recorded separately.
