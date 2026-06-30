# Runtime image for the LeanSignal Agent.
#
# This Dockerfile is built by goreleaser, which cross-compiles the binary first
# and places it (plus the files listed in .goreleaser.yaml `extra_files`) into the
# build context. distroless/base already ships CA certificates, so there is no
# separate certs stage.
FROM gcr.io/distroless/base-debian12:nonroot

COPY leansignal-agent /leansignal-agent
COPY config/agent-config.example.yaml /etc/leansignal-agent/config.yaml

# OTLP gRPC, OTLP HTTP, health check
EXPOSE 4317 4318 13133

ENTRYPOINT ["/leansignal-agent"]
CMD ["--config", "/etc/leansignal-agent/config.yaml"]
