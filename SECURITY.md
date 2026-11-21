# Security Policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
("Report a vulnerability" under this repository's **Security** tab).
Please don't open a public issue for security problems.

## Supported versions

Only the latest release is supported. The project is pre-1.0 and changes quickly.

## Scope

Areas of particular interest:

- the server-side URL fetcher (SSRF protections, `internal/safefetch`);
- trusted-header authentication and the trusted-proxy boundary;
- image upload handling;
- the capture API and its tokens.
