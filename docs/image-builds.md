# Git image builds

A Git-linked Compose file can use `build:`. The target server's agent checks
out the recorded commit and builds each service image before the deployment
is dispatched. Enable this per server with `allow_builds: true` in the agent
configuration. Builds run one at a time on each agent.

An admin can select **Deploy from repository** on a connected Git repository
to create a deployment when the repo has a Dockerfile but no Compose file.
The wizard checks the Dockerfile, generates a Git-linked Compose file, and
deploys it to the selected server. Environment values are stored with the
deployment; the generated Compose file contains only variable references.
When the branch moves, its generated service settings stay in place while
the commit used for the next build advances. The server must opt into builds.

When the server has the Docker CLI and its Buildx plugin, the agent builds
with BuildKit and loads the resulting image into that server's Docker Engine.
An Engine-only server uses the classic builder. The build log says which path
was used. Install Buildx on the agent host to use Dockerfile features such as
`RUN --mount`; those Dockerfile features will fail on the classic path.

An admin can open a successful build in the deployment's **Builds** tab and
choose **Push to registry**. Select a configured registry and a repository
path within it. The control plane constructs an immutable tag from the local
build tag and sends registry credentials to the target agent for that push.
The deployment continues to use its local image. The published reference is
returned to the app and can be used as an image reference elsewhere.

The push action is manual. It requires the target agent to be connected,
`allow_builds: true`, and the image to still exist on that server. Builds do
not yet publish automatically or scan the published image automatically.
