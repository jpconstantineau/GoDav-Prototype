# HTTPS discovery contract (slice 03)

Issue: https://github.com/jpconstantineau/GoDav-Prototype/issues/4.
Base: 7110b8fd2569bca3ec043f034f1290188082b0c9; existing-state foundation.

## Approved scope and seams

Explicit `godav run server --config <path>` only; no default config, initialization,
migration, account issuance, real certificate/trust installation, content writes,
UI or service work. Existing private YAML and SQLite are opened ReadOnly through
localstate.OpenExisting, retaining exclusive ownership and the validated account
connection until requests stop. HTTP backed by temporary real SQLite and actual
CLI processes are the approved tests. No private-helper acceptance tests.

Go 1.27.4, module baseline 1.27.0, process-local verified tooling and no automatic
toolchain downloads. Prefer current language/stdlib features before dependencies:
net/http ServeMux, netip/url, tls/x509, subtle, slog, context/channels. x/net/webdav
is justified only for actual DAV parsing/serialization after its separate audit;
no router, auth or logging framework. Foundation files remain coordinator-owned.

## Startup

Usage errors exit 2, operational failures 1. Require explicit existing config,
exact `/dav/`, Basic, loopback HTTPS, valid stored enabled accounts, protected
TLS paths and matching unexpired certificate/key with the origin hostname and
server usage. Serving does not establish Windows client trust. No fallback.
Reject httpmuxgo121=1 because mux compatibility is selected before main runs.
Bind only after prerequisites pass. Emit one ready line with the same revision,
build-time and source identity as version only after a successful bind.

## HTTP

Authority and raw-path checks precede ServeMux. Compare case-insensitive DNS or
canonical IP plus numeric effective port; no DNS lookup or forwarded-header trust.
No localhost/IP alias equivalence. Unexpected Host or absolute-form authority,
userinfo, malformed/singular Origin or Origin mismatch is 400. Missing Origin is
normal. OPTIONS * reaches application handling, never net/http automatic success.
Only origin-form paths are accepted. Unrelated and near-prefix paths return 404.
Raw/decoded traversal, separators, repeated slashes, backslashes, controls and
noncanonical mount spelling are rejected before mux cleaning; no redirect.

Both `/dav` and `/dav/` authenticate and discover the same root; href is `/dav/`.
Unauthenticated protected requests receive 401 and Basic realm="GoDav". Duplicate,
malformed, absent, unknown, disabled or incorrect credentials receive the same
challenge. Username matching uses the existing ASCII case-insensitive policy;
passwords are byte-exact, bounded, and verified using validated Argon2id records
and constant-time hash comparison. No writes or hash upgrades. Concurrent hash
work is bounded without an unbounded queue; overload is 503. This is not the
complete admission/shutdown feature of slice 11.

Authenticated OPTIONS returns 200, Allow `OPTIONS, PROPFIND`. The partial slice
makes no DAV class-compliance or lock/write claim. PROPFIND Depth 0/1 yields 207
with one collection and no children. Infinity/omitted Depth is 403 with
DAV:propfind-finite-depth; malformed depth/XML is 400; bounded XML overflow 413.
Empty body means allprop, and explicit missing properties are property-level 404.
Unknown paths return 404; unsupported methods return 405 with the narrow Allow.
Present Destination must be singular, absolute HTTPS, query/fragment/userinfo-free,
same authority and confined path. Malformed=400, other origin=502, outside DAV=403.
Validation never fetches a destination; unsupported valid operations still get 405.

## Safety and evidence

One slog completion wraps all application handling including 401 and rejections.
Allowlist fields only: time, method, sanitized path, status, duration, request ID.
No raw URLs, queries, headers, credentials, tokens, bodies or low-level errors.
Transport failures before the handler produce a fixed generic transport message;
they are not fabricated DAV completion records. x/net's separate Logger is nil.
Automated TLS roots stay in each test client's cert pool; never skip verification
or alter Windows trust. Fixtures and secrets stay disposable and memory-only.

Each behavior is built one failing test then minimal implementation. Record red,
green, aggregate native tests, format/vet/module checks, target source/binary
scans and exact release identity. Linux cross-build is not Linux runtime proof;
a race-enabled harness is not evidence of race-instrumented CLI children.

## Exact initial transport limits and dependency decision

HTTP/1.1 only is selected through Go's stdlib http.Protocols. TLS 1.2 minimum;
client trust remains external. Header byte/count limits are 16 KiB / 100 values;
header-read timeout 5s, request read/write 10s, idle 30s. Four admitted authenticated
requests bound password hashing and root-discovery work; no waiting queue. XML
body limit 64 KiB, depth 32 and 256 elements; response buffer limit 1 MiB. These
conservative prototype constants are not a replacement for slice 11's configurable
admission/staging/lifecycle contract. Closed transport connections are followed by
joining admitted handlers before closing SQLite/ownership.

Pin x/net v0.62.0 following the separate 2026-11-14 importing-DAV audit. It adds
no change to existing selected pins. Preserve BSD license and PATENTS notice.
Use upstream PROPFIND parsing/XML serialization over a private empty NewMemFS,
never webdav.Dir. A bounded compatibility step empties DAV:supportedlock; no
mutation method reaches the filesystem or its required internal LockSystem.
The empty root is synthetic; real file persistence begins in the next slice.
Upstream is explicitly not hardened against arbitrary malicious clients, so
outer authentication, path/method/depth/body/work bounds remain application duties.
