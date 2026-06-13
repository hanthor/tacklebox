# Minimal live-bootable bootc image for the CI ISO smoke tests.
#
# dracut-live provides the dmsquash-live dracut module that live ISO boots
# need; tacklebox's automatic initramfs preparation rebuilds the initramfs
# with it (plus tbox-root) at build time — but only modules INSTALLED in
# the image can be added, hence the dnf install here. Stock minimal bootc
# images don't ship dracut-live.
FROM quay.io/fedora/fedora-bootc:44
RUN dnf -y install dracut-live && dnf clean all

# Per-env marker, layered AFTER the shared dnf layer so the two fixture
# builds (alpha/beta) differ only by this file: the verify distinctness
# checks need distinct content, and the dedup smoke needs almost-identical
# content to prove cross-env dedup shrinks the ISO.
ARG MARKER=dev
RUN echo "$MARKER" > /usr/share/tbox-env-marker
