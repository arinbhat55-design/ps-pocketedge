# Volume files

Open **Volumes → a volume → Browse files** to list directories and inspect files on the selected server. You can download any regular file up to 1 MiB. An administrator can edit a text file or upload a file of up to 1 MiB after all containers using that volume have stopped. **Clone volume** copies an unused volume to a new named volume on the same server; the copy streams through the Docker daemon and agent without loading the volume into the control plane.

The agent mounts the volume in a temporary Docker helper container. File paths are restricted to the volume root, and symlink paths cannot be opened. The helper has no network access and is removed when the operation finishes. The agent checks that the volume is unused before writes or cloning. A server needs access to the `busybox:latest` helper image.

The individual-file limit keeps the request within the agent stream message size. Whole-volume export and import need a separate streaming archive transfer and are still pending. Deployment volume backup and restore remain available through the existing backup feature.
