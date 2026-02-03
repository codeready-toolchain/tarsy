#!/bin/bash
set -e

# Script to regenerate protobuf files for both Go and Python

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PROTO_DIR="$PROJECT_ROOT/proto"
PROTO_FILE="$PROTO_DIR/llm_service.proto"

echo "==> Generating Go protobuf files..."
cd "$PROJECT_ROOT"
protoc \
  --go_out=. \
  --go_opt=paths=source_relative \
  --go-grpc_out=. \
  --go-grpc_opt=paths=source_relative \
  "$PROTO_FILE"

echo "==> Generating Python protobuf files..."
cd "$PROJECT_ROOT/llm-service"

# Generate into dedicated proto package
python -m grpc_tools.protoc \
  -I"$PROTO_DIR" \
  --python_out=proto \
  --grpc_python_out=proto \
  --pyi_out=proto \
  "$PROTO_FILE"

# Fix the import in the generated grpc file to use relative import
# (protoc generates absolute imports by default, which don't work in packages)
sed -i 's/^import llm_service_pb2/from . import llm_service_pb2/' proto/llm_service_pb2_grpc.py

echo "==> Proto files generated successfully!"
echo ""
echo "Generated files:"
echo "  - proto/llm_service.pb.go"
echo "  - proto/llm_service_grpc.pb.go"
echo "  - llm-service/proto/llm_service_pb2.py"
echo "  - llm-service/proto/llm_service_pb2_grpc.py"
echo "  - llm-service/proto/llm_service_pb2.pyi"
