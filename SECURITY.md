# Security Policy

## Supported Versions

We use [Semantic Versioning](https://semver.org/).

| Version | Supported |
|---------|-----------|
| 5.x.x   | yes       |
| < 5.x   | no        |

Bug fix releases (`5.0.1`, `5.0.2`) are cut as needed from the latest minor line.
Each version is supported until the next one ships.

## Reporting a Vulnerability

If you've discovered a security vulnerability in db-backup, please report it
responsibly via [GitHub Security Advisories](https://github.com/nfrastack/db-backup/security/advisories/new).

**Please do not open a public issue for security vulnerabilities.**

You can expect an initial response within 72 hours. We will work with you to
understand the issue, develop a fix, and coordinate disclosure timing.

### What to include

- Affected version(s) and component (dump, restore, scheduler, license, storage backend)
- Steps to reproduce or proof of concept
- Impact assessment (data exposure, privilege escalation, credential leak, etc.)
- Any known workarounds

## Scope

db-backup handles credentials, connects to database servers, encrypts and
transports backup data, and runs inside containers. The following are in scope:

- Credential exposure (hardcoded secrets, improper env/file handling, log leakage)
- SQL injection or command injection through configuration values
- Authentication bypass or privilege escalation in engine connections
- Backup integrity (unauthorized modification of dump data or sidecar metadata)
- License bypass or spoofing
- Container escape or unsafe default permissions
- Supply chain issues in Go dependencies

Out of scope: misconfigured databases, weak passwords chosen by operators,
or vulnerabilities in upstream database engines themselves.

## Acknowledgements

We credit security researchers who responsibly disclose vulnerabilities in
release notes upon request.
