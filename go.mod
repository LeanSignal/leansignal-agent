module github.com/leansignal/leansignal-agent

go 1.24.0

toolchain go1.24.1

require (
	github.com/zeebo/xxh3 v1.1.0
	go.opentelemetry.io/collector/component v1.47.0
	go.opentelemetry.io/collector/consumer v1.47.0
	go.opentelemetry.io/collector/extension v1.47.0
	go.opentelemetry.io/collector/pdata v1.47.0
	go.opentelemetry.io/collector/processor v1.47.0
	go.uber.org/zap v1.28.0
	google.golang.org/grpc v1.77.0
)

require (
	golang.org/x/net v0.46.1-0.20251013234738-63d1a5100f82 // indirect
	golang.org/x/text v0.30.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251022142026-3a174f9686a8 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)

require (
	github.com/hashicorp/go-version v1.7.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/leansignal/leansignal-agent/proto v0.0.0
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	go.opentelemetry.io/collector/featuregate v1.47.0 // indirect
	go.opentelemetry.io/collector/pipeline v1.47.0 // indirect
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/metric v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.38.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sys v0.37.0 // indirect
)

replace github.com/leansignal/leansignal-agent/proto => ./proto
