#!/bin/bash
DISPLAY_NUM="$1"
RFB_PORT="$2"
RESOLUTION="${VNC_RESOLUTION:-1920x1080}"
DEPTH="${VNC_DEPTH:-24}"

exec /usr/bin/Xvnc ":${DISPLAY_NUM}" \
    -geometry "${RESOLUTION}" \
    -depth "${DEPTH}" \
    -rfbport "${RFB_PORT}" \
    -SecurityTypes None \
    -AlwaysShared \
    -AcceptSetDesktopSize \
    -localhost no
