# Security

How to report security vulnerabilities in db-backup responsibly.

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Use one of these private channels:

- **Email**: [security@nfrastack.com](mailto:security@nfrastack.com?subject=%5BSECURITY%5D%20db-backup%20vulnerability%20report) with a `[SECURITY]` subject prefix
- **GitHub private vulnerability reporting**: [Report a vulnerability](https://github.com/nfrastack/db-backup/security/advisories/new) on the repository Security tab

Both land in the same place. Email is monitored, the GitHub advisory system keeps everything off the public tracker until a fix is ready.

## What to include

- Affected db-backup version (`dbb version`)
- Platform and architecture (eg `linux/amd64`, container vs bare binary)
- Steps to reproduce or a proof of concept
- Impact assessment - what an attacker could do with this
- Any relevant configuration (redact credentials and hostnames)

You do not need to be certain it is exploitable. If something looks wrong, report it.

## Response timeline

| Stage             | Target                                  |
| ----------------- | --------------------------------------- |
| Acknowledgement   | 48 hours                                |
| Status update     | 7 days                                  |
| Fix or mitigation | Best effort, communicated during triage |

## Disclosure policy

Coordinated disclosure. Once a fix is available we will publish an advisory with credit to you unless you prefer to remain anonymous. We ask that you allow up to 90 days from initial report before public disclosure so users have time to patch.

## Scope

The following are in scope:

- The `dbb` binary and its container image
- Encryption implementation (age, GPG/OpenPGP, OpenSSL)
- Configuration parsing and secret resolution (`file://`, `env://`)
- License verification mechanism
- Container entrypoint and init scripts

The following are out of scope:

- Social engineering of Nfrastack staff or users
- Denial of service attacks against public infrastructure
- Physical attacks on hardware
- Vulnerabilities in third party dependencies that are not reachable through db-backup

## Supported versions

Security fixes are applied to the latest release only. Older versions should upgrade.

## PGP

If you prefer to send an encrypted report, use this public key:

```
-----BEGIN PGP PUBLIC KEY BLOCK-----

mDMEao5M4xYJKwYBBAHaRw8BAQdAS89LMRi6PgsNoZOSYcrw7RHXl7MqjklQ4+lB
lB/L4DG0K05mcmFzdGFjayBTZWN1cml0eSA8c2VjdXJpdHlAbmZyYXN0YWNrLmNv
bT6IlgQTFgoAPhYhBECYxkkaS7zjJcUfWl6+RL+jml7GBQJqjkzjAhsDBQkJZgGA
BQsJCAcCBhUKCQgLAgQWAgMBAh4BAheAAAoJEF6+RL+jml7Gq1sA/0dNrsCNMv76
MuaPcak9T6+AprWXni3CU4yDpRYeDBekAP9y85wsm7dUVV1Sj3QOOBhAIkwtRGkf
LVZKljWQ3cnHArg4BGqOTOMSCisGAQQBl1UBBQEBB0AUQGyiAQox6wqUprPcfpYw
+dzOxxZhwMZ+cg6LDIn4TAMBCAeIfgQYFgoAJhYhBECYxkkaS7zjJcUfWl6+RL+j
ml7GBQJqjkzjAhsMBQkJZgGAAAoJEF6+RL+jml7Gr3sBAJ/fpOJ6JBpvO6izvaX8
XCp34BI6VDfOrfBLEI1Lakv9AP9h4Xp9YWo1N5UP4OAJuwpi9ypYEyXtCb0gggYu
Dgs1AA==
=/LEX
-----END PGP PUBLIC KEY BLOCK-----
```
