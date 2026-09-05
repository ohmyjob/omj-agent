# Security policy

## Reporting a vulnerability

Email `security@ohmyjob.sh`. Please do not open a public issue, and please do
not post it anywhere public before we have had a chance to fix it.

Helpful to include:

- what you found and what an attacker can do with it;
- the versions you tested, from `omj-agent version`, and the Server version
  from its Settings page;
- the operating system and how the Agent was installed;
- the steps to reproduce it, with a proof of concept if you have one;
- whether you have told anyone else.

You will get an acknowledgement within a few days. We will tell you what we
think the impact is, whether we agree it is a vulnerability, and roughly when a
fix will land. When it does, we will credit you in the release notes unless you
prefer otherwise.

Please do not include a credential, a token or a real log in the report. If
reproducing needs one, say so and we will arrange it.

## Supported versions

Oh My Job is pre-1.0. Fixes go into the latest release only; there are no
backports to earlier versions. Upgrade to the newest release before reporting
something you found on an old one, in case it is already fixed.

## Scope

In scope: the Agent, the protocol between it and the Server, the installer at
`packaging/install.sh` (served as `https://ohmyjob.sh/install.sh`), the
systemd unit and the published release archives.

The Server, its container image and its web interface belong to
[`omj-server`](https://github.com/ohmyjob/omj-server); report those the same
way.

Worth reading before reporting: [docs/security.md](docs/security.md) describes
the trust model. Some things that look like vulnerabilities are the design
stated plainly.

In particular, an administrator of the Server can run arbitrary commands on
every enrolled machine, as that machine's Agent user, immediately. That is what
the product does. Reports that amount to "an administrator can schedule a
command", or that a Job can read files the service user is allowed to read, are
not vulnerabilities.

A report that the Server can make the Agent exceed the privileges of its own
operating-system user, on the other hand, is exactly the kind of thing we want
to hear about. So is anything that lets a lease escape the limits in
`agent.conf`, exposes the credential, weakens the installer's checksum
verification, or downgrades the connection to the Server.
