# Security

Kubehack acts with your Kubernetes access, so security problems matter. Please
don't report them in public issues.

## Report a problem

[Open a private report](https://github.com/Thadchet/kubehack/security/advisories/new)
on GitHub. Include what you found, how to reproduce it, and which version you use
(Mac, Windows or Docker). Leave out real tokens, kubeconfig files and Secret values.

This is a personal project, so replies aren't guaranteed by a set date.

## Supported versions

Fixes go into the latest release only.

## What counts

For example:

- A web page or another local program reaching Kubehack's API
- A change getting past **Read-only**
- A credential or Secret value leaving your computer or appearing in logs
- Anything in the Docker image that exposes your cluster access

The Docker link holds a secret token, and Kubehack has the same access as your
kubeconfig. Sharing that link, or publishing the port beyond `127.0.0.1`, isn't a
vulnerability in Kubehack.
