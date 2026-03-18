package orchestrator

import (
	"archive/tar"
	"bytes"
	"context"
	"log"
	"path/filepath"

	"github.com/docker/docker/api/types/container"
)

// patchForHostDisplay uses CopyToContainer to overwrite s6 service scripts
// BEFORE the container starts, so Xtigervnc does not launch and Chromium
// gets GPU flags for the host X display.
func (d *DockerOrchestrator) patchForHostDisplay(ctx context.Context, containerID string) {
	xvncScript := []byte("#!/command/with-contenv bash\n# GPU mode: host provides DISPLAY=:0\nexec sleep infinity\n")

	desktopScript := []byte(`#!/command/with-contenv bash

export HOME=/home/claworc
export LANG=en_US.UTF-8

# Copy Xauthority so claworc user can read it
if [ -f "$XAUTHORITY" ]; then
  cp "$XAUTHORITY" /tmp/.claworc-Xauthority
  chmod 644 /tmp/.claworc-Xauthority
  export XAUTHORITY=/tmp/.claworc-Xauthority
fi

# Wait for host X server
while ! xdpyinfo >/dev/null 2>&1; do sleep 0.5; done

# Start openbox window manager
s6-setuidgid claworc openbox &

EXTRA_ARGS=()
if [ -n "$CHROMIUM_USER_AGENT" ]; then
  EXTRA_ARGS+=("--user-agent=$CHROMIUM_USER_AGENT")
fi

if command -v google-chrome-stable >/dev/null 2>&1; then
  BROWSER=google-chrome-stable
elif command -v brave-browser >/dev/null 2>&1; then
  BROWSER=brave-browser
else
  BROWSER=/usr/bin/chromium
fi

while true; do
  s6-setuidgid claworc $BROWSER \
    --no-first-run \
    --password-store=basic \
    --simulate-outdated-no-au='Tue, 31 Dec 2099 23:59:59 GMT' \
    --start-maximized \
    --user-data-dir=/home/claworc/chrome-data \
    --remote-debugging-port=9222 \
    --remote-debugging-address=127.0.0.1 \
    --disable-default-apps \
    --disable-features=CloseWindowWithLastTab \
    --disable-blink-features=AutomationControlled \
    --disable-infobars \
    --disable-component-update \
    --load-extension=/opt/stealth-extension \
    --no-sandbox \
    --enable-gpu \
    --use-gl=egl \
    --ignore-gpu-blocklist \
    --enable-features=Vulkan,VaapiVideoDecoder \
    --enable-gpu-rasterization \
    "${EXTRA_ARGS[@]}" > /dev/null 2>&1
  sleep 1
done
`)

	d.copyFileToContainer(ctx, containerID, "/etc/s6-overlay/s6-rc.d/svc-xvnc/run", xvncScript, 0755)
	d.copyFileToContainer(ctx, containerID, "/etc/s6-overlay/s6-rc.d/svc-desktop/run", desktopScript, 0755)
}

func (d *DockerOrchestrator) copyFileToContainer(ctx context.Context, containerID, destPath string, content []byte, mode int64) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: filepath.Base(destPath),
		Mode: mode,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		log.Printf("Failed to write tar header for %s: %v", destPath, err)
		return
	}
	if _, err := tw.Write(content); err != nil {
		log.Printf("Failed to write tar content for %s: %v", destPath, err)
		return
	}
	tw.Close()
	err := d.client.CopyToContainer(ctx, containerID, filepath.Dir(destPath), &buf, container.CopyToContainerOptions{})
	if err != nil {
		log.Printf("Failed to copy %s to container: %v", destPath, err)
	}
}
