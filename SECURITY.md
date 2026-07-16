# Security Policy

## Reporting a vulnerability

Please **do not** open public GitHub issues for security vulnerabilities.

Report security issues privately via one of:

- GitHub's [private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
  ("Report a vulnerability" under the repository's **Security** tab), or
- email **security@leansignal.com** <!-- TODO: confirm/replace this address -->

Please include:

- a description of the issue and its impact,
- steps to reproduce (a proof of concept if possible),
- affected version(s) / commit, and
- any suggested remediation.

We aim to acknowledge reports within a few business days and will keep you
updated on remediation progress. We support coordinated disclosure and will
credit reporters who wish to be named.

## Supported versions

Until a `1.0.0` release, security fixes are applied to the latest released
`0.x` version. Pin to released tags and upgrade promptly when advisories are
published.

## Scope

This repository packages an OpenTelemetry Collector distribution and ships the
agent alongside co-located telemetry stores: it bundles VictoriaMetrics binaries
downloaded from upstream, and its installer pulls Loki and Tempo from their
upstream releases. Vulnerabilities in upstream OpenTelemetry, VictoriaMetrics,
Loki, or Tempo code should also be reported to their respective maintainers; we
will track and incorporate upstream fixes.
