#!/bin/bash
set -e

# Workaround for potential buildx permission issues
echo "Setting up isolated build environment..."
export TMPDIR=/tmp
export DOCKER_CONFIG=$(mktemp -d /tmp/docker-config-XXXXXX)
cp ~/.docker/config.json "$DOCKER_CONFIG/" 2>/dev/null || true
if [ -d "$HOME/.docker/contexts" ]; then
    cp -r "$HOME/.docker/contexts" "$DOCKER_CONFIG/" 2>/dev/null || true
fi

# Cleanup on exit
trap "rm -rf $DOCKER_CONFIG" EXIT

IMAGE_NAME="glukw/openclaw-vnc-chromium"
TEST_TAG="test-build"
CONTAINER_NAME="agent-test-container"

echo "Building local image for testing..."
# Use buildx explicitly or let docker build perform as is, but now with isolated config
docker build -t $IMAGE_NAME:$TEST_TAG .

echo "Starting test container..."
# Remove any existing test container
docker rm -f $CONTAINER_NAME 2>/dev/null || true

# Run container in detached mode with token (no systemd needed)
# Map port 3000 for local access test
docker run -d --name $CONTAINER_NAME -e OPENCLAW_GATEWAY_TOKEN=testtoken -p 3000:3000 $IMAGE_NAME:$TEST_TAG

echo "Waiting for services to start..."

# Wait loop for Openclaw Gateway process
MAX_RETRIES=12
for i in $(seq 1 $MAX_RETRIES); do
    if docker exec $CONTAINER_NAME pgrep -f "openclaw gateway" > /dev/null; then
        echo "✅ Openclaw Gateway process is running."
        break
    fi
    if [ $i -eq $MAX_RETRIES ]; then
        echo "❌ Openclaw Gateway failed to start after $((MAX_RETRIES * 5)) seconds."
        docker logs $CONTAINER_NAME
        # docker rm -f $CONTAINER_NAME
        exit 1
    fi
    echo "Waiting for Openclaw Gateway... ($i/$MAX_RETRIES)"
    sleep 5
done

echo "Verifying Webtop/KasmVNC on port 3000..."
# Webtop uses port 3000 by default (HTTPS)
# Use -k to skip certificate validation if needed, or http if it supports it
if curl -s -k -f https://localhost:3000/ > /dev/null || curl -s -f http://localhost:3000/ > /dev/null; then
    echo "✅ Webtop UI is listening on port 3000."
else
    echo "❌ Webtop UI is NOT responding on port 3000."
    docker logs $CONTAINER_NAME
    # docker rm -f $CONTAINER_NAME
    exit 1
fi

echo "Extracting Openclaw version..."
# Extract version directly from package.json since npm CLI might be unstable in the minimal image
VERSION=$(docker exec $CONTAINER_NAME cat /usr/lib/node_modules/openclaw/package.json | grep '"version":' | head -1 | awk -F'"' '{print $4}')

if [ -z "$VERSION" ]; then
    echo "❌ Could not detect Openclaw version."
    docker rm -f $CONTAINER_NAME
    exit 1
fi

echo "✅ Detected Openclaw version: $VERSION"

echo "Stopping test container..."
docker rm -f $CONTAINER_NAME

echo "Ready to push version $VERSION to Docker Hub."
echo "Proceeding with multi-arch build and push..."

echo "Building and pushing multi-arch image provided check passed..."

BUILDER_NAME="agent-builder-$(date +%s)"
if ! docker buildx create --name "$BUILDER_NAME" --driver docker-container --use --bootstrap; then
    echo "Failed to create builder directly. Attempting to clean run..."
    docker buildx rm "$BUILDER_NAME" 2>/dev/null || true
    docker buildx create --name "$BUILDER_NAME" --driver docker-container --use --bootstrap
fi
# Update trap to include builder removal
trap "docker buildx rm $BUILDER_NAME 2>/dev/null || true; rm -rf $DOCKER_CONFIG" EXIT

docker buildx build --platform linux/amd64,linux/arm64 \
    -t $IMAGE_NAME:$VERSION \
    -t $IMAGE_NAME:latest \
    --push .

echo "✅ Successfully pushed $IMAGE_NAME:$VERSION and $IMAGE_NAME:latest"
