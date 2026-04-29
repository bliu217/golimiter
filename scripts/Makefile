gen: 
	@cd .. && protoc \
		--proto_path=api/proto api/proto/limiter.proto \
		--go_out=generated/proto/limiter \
		--go_opt=paths=source_relative \
		--go-grpc_out=generated/proto/limiter \
		--go-grpc_opt=paths=source_relative