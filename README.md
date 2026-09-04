# Oh My Job Agent

Run this command, on this machine, at this time, and know what happened.

The Agent enrolls a machine with an Oh My Job server, polls it for work, runs the commands it is given and reports the output and the result back. It is one static binary for Linux that runs under systemd.

## Development

```sh
make build
make test lint
```
