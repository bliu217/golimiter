gen: 
	@cd .. && protoc \
		--proto_path=api/proto api/proto/limiter.proto \
		--go_out=generated/proto/limiter \
		--go_opt=paths=source_relative \
		--go-grpc_out=generated/proto/limiter \
		--go-grpc_opt=paths=source_relative

test-unit:
	@cd .. && go test ./...

test-integration:
	@cd .. && go test -tags=integration ./...

clean-sim-summaries:
	@cd .. && rm -f cmd/sim/summaries/*.json