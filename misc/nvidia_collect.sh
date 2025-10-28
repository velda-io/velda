#!/bin/bash
set -euo pipefail

FILELIST=(
	libnvidia-ml.so
	libnvidia-cfg.so
	libcuda.so
	libcudadebugger.so
	libnvidia-opencl.so
	libnvidia-gpucomp.so
	libnvidia-ptxjitcompiler.so
	libnvidia-allocator.so
	libnvidia-pkcs11.so
	libnvidia-pkcs11-openssl3.so
	libnvidia-nvvm.so
	libvdpau_nvidia.so
	libnvidia-encode.so
	libnvidia-opticalflow.so
	libnvcuvid.so
	libnvidia-eglcore.so
	libnvidia-glcore.so
	libnvidia-tls.so
	libnvidia-glsi.so
	libnvidia-fbc.so
	libnvidia-rtcore.so
	libnvoptix.so
	libGLX_nvidia.so
	libEGL_nvidia.so
	libGLESv2_nvidia.so
	libGLESv1_CM_nvidia.so
	libnvidia-glvkspirv.so
)

DEST=/var/nvidia/lib
sudo mkdir -p "$DEST"

declare -A processed

# Helper: bind a source file into a destination file readonly. If already mounted, skip.
bind_file_readonly() {
	local src="$1" dest="$2"

	# resolve source to its realpath if possible
	if real_src=$(realpath -e -- "$src" 2>/dev/null); then
		src="$real_src"
	fi

	# ensure dest directory exists
	sudo mkdir -p "$(dirname "$dest")"
    ln "$src" "$dest"
}

for FILE in "${FILELIST[@]}"; do
	# skip duplicates
	if [[ -n "${processed[$FILE]:-}" ]]; then
		continue
	fi

	# Query ldconfig cache: find the first library name that starts with the FILE prefix.
	# We print the found library name and the resolved path separated by '|'.
	entry=$(ldconfig -p 2>/dev/null | awk -v f="$FILE" '$1 ~ ("^" f) {print $1 "|" $NF; exit}') || true

	if [[ -z "$entry" ]]; then
		echo "Warning: '$FILE' not found in ldconfig cache; skipping" >&2
		processed[$FILE]=1
		continue
	fi


	soname="${entry%%|*}"
	srcpath="${entry#*|}"
	# prefer the realpath basename (resolve symlinks)
	real_src=$(realpath -e -- "$srcpath" 2>/dev/null || true)
	if [[ -n "$real_src" ]]; then
		filename=$(basename "$real_src")
	else
		filename=$(basename "$srcpath")
	fi

	echo "Collecting: prefix='$FILE'  soname='$soname'  file='$filename'  src='$srcpath' (real='$real_src')"

	# Bind-mount the real file into the destination (readonly)
	bind_file_readonly "${real_src:-$srcpath}" "$DEST/$filename"

	# Create symlinks inside DEST:
	#   soname -> full filename
	#   shortcut (FILE) -> soname
	pushd "$DEST" >/dev/null
	# If soname equals filename no need to link; otherwise make soname point to filename
	if [[ "$soname" != "$filename" ]]; then
		sudo ln -sf "$filename" "$soname"
	fi

	# Make the shortcut (e.g. libcuda.so) point to the soname
	if [[ "$FILE" != "$soname" ]]; then
		sudo ln -sf "$soname" "$FILE"
	fi
	popd >/dev/null

	processed[$FILE]=1
done

echo "Done. Collected libraries are in: $DEST"

# ---- Binaries collection ----
# BINLIST: list of binary names to locate via PATH and copy into $DEST_BIN
BINLIST=(
	nvidia-smi
	nvidia-debugdump
	nvidia-persistenced
	nv-fabricmanager
	nvidia-cuda-mps-control
	nvidia-cuda-mps-server
)

DEST_BIN=/var/nvidia/bin
sudo mkdir -p "$DEST_BIN"

for BIN in "${BINLIST[@]}"; do
	# avoid duplicates
	if [[ -n "${processed[$BIN]:-}" ]]; then
		continue
	fi

	# Locate binary via PATH
	binpath=$(command -v "$BIN" 2>/dev/null || true)

	if [[ -z "$binpath" ]]; then
		echo "Warning: binary '$BIN' not found in PATH; skipping" >&2
		processed[$BIN]=1
		continue
	fi


		# determine realpath basename for binary as well
		real_bin=$(realpath -e -- "$binpath" 2>/dev/null || true)
		if [[ -n "$real_bin" ]]; then
			bfilename=$(basename "$real_bin")
		else
			bfilename=$(basename "$binpath")
		fi
		echo "Collecting binary: name='$BIN'  src='$binpath'  file='$bfilename' (real='$real_bin')"

		# Bind-mount the binary readonly into DEST_BIN
		bind_file_readonly "${real_bin:-$binpath}" "$DEST_BIN/$bfilename"

	processed[$BIN]=1
done

echo "Done. Collected binaries are in: $DEST_BIN"