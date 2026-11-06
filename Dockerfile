# Both build stages run on the builder's own platform, because emulating another
# one is slow. Only the Go binary depends on the target.
FROM --platform=$BUILDPLATFORM node:22-alpine AS ui
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS server
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/kubehack-docker cmd/kubehack-docker
COPY internal internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /kubehack-docker ./cmd/kubehack-docker

# The default for clusters that need no credential plugin.
FROM alpine:3.22 AS kubehack
RUN apk add --no-cache ca-certificates && install -d -o nobody /settings
COPY --from=ui /src/frontend/dist /app/ui
COPY --from=server /kubehack-docker /app/kubehack-docker
# Any user can run this image, so HOME has to be writable by all of them.
ENV HOME=/tmp KUBECONFIG=/kube/config
USER nobody
EXPOSE 8765
ENTRYPOINT ["/app/kubehack-docker"]

# For GKE, whose credential plugin needs gcloud.
FROM gcr.io/google.com/cloudsdktool/google-cloud-cli:alpine AS kubehack-gke
RUN gcloud components install gke-gcloud-auth-plugin --quiet \
  && rm -rf /google-cloud-sdk/.install/.backup \
  && install -d -o nobody /settings
COPY --from=ui /src/frontend/dist /app/ui
COPY --from=server /kubehack-docker /app/kubehack-docker
ENV HOME=/tmp KUBECONFIG=/kube/config CLOUDSDK_CONFIG=/gcloud
USER nobody
EXPOSE 8765
ENTRYPOINT ["/app/kubehack-docker"]
