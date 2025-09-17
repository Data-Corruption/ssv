#!/bin/sh

# Target: POSIX Linux x86_64/amd64, run-in-place verilator install
# Example: curl -fsSL https://raw.githubusercontent.com/Data-Corruption/ssv/main/scripts/install_verilator.sh | sh [-s [version=<version>] [threads=<n>]]

set -u
umask 077

# Template variables ----------------------------------------------------------
APP_NAME="ssv"

# Constants -------------------------------------------------------------------
VER_REPO="https://github.com/verilator/verilator"
VER_ROOT_DIR="$HOME/.$APP_NAME/verilator"
VER_BIN="$VER_ROOT_DIR/bin/verilator"
BUILD_LOG="$VER_ROOT_DIR/build.log"

export VERILATOR_ROOT="$VER_ROOT_DIR"

printSuccess() { printf '\033[32m%s\033[0m\n' "$@"; }
printWarn() { printf '\033[33m%s\033[0m\n' "$@" >&2; }
printErr() { printf '\033[31m%s\033[0m\n' "$@" >&2; }
fatal() { printErr "$@"; exit 1; }

_hide_cursor() {
	if [ -t 1 ]; then
		if command -v tput >/dev/null 2>&1; then tput civis 2>/dev/null || printf '\033[?25l'; else printf '\033[?25l'; fi
		CURSOR_HIDDEN=1
	fi
}
_show_cursor() {
	if [ "${CURSOR_HIDDEN:-}" = "1" ]; then
		if command -v tput >/dev/null 2>&1; then tput cnorm 2>/dev/null || printf '\033[?25h'; else printf '\033[?25h'; fi
		CURSOR_HIDDEN=
	fi
}

cleanup() { _show_cursor; }
on_exit() { status=$?; cleanup; return; }

trap 'on_exit' EXIT
trap 'printWarn "Aborted"; [ -n "${_cmd_pid:-}" ] && kill $_cmd_pid 2>/dev/null; [ -n "${_spin_pid:-}" ] && kill $_spin_pid 2>/dev/null; exit 130' INT TERM HUP QUIT

# Parse Args ------------------------------------------------------------------
DEFAULT_VERSION="v5.006"
version="$DEFAULT_VERSION"
threads=0 # 0 = auto-detect

usage() {
	cat <<EOF
Usage: ${0##*/} [version=<version>] [threads=<n>] [-h|--help]
  version=<version>   Verilator version (e.g. v5.020) or 'latest'. Default: ${DEFAULT_VERSION}
  threads=<n>         Number of threads to use for build (default: auto-detect)
  -h, --help          Show this help message and exit
EOF
}

while [ $# -gt 0 ]; do
	case $1 in
	-h | --help) usage; exit 0 ;;
	version=*)
		version=${1#version=}
        [ -n "$version" ] || fatal "error: version= must not be empty"
		;;
	threads=*)
		threads=${1#threads=}
		[ -n "$threads" ] || fatal "error: threads= must not be empty"
		;;
	*=*)
		key=${1%%=*}
		fatal "error: unknown option '$key=...'"
		;;
	-*) fatal "error: unknown flag '$1'" ;;
	*) fatal "error: positional args are not supported ('$1')" ;;
	esac
	shift
done

[ "$version" = "latest" ] || printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]{3}$' || fatal "invalid version: $version"

case $threads in
'' | *[!0-9]*) fatal "error: threads= must be an integer >= 0 (0=auto-detect)" ;;
esac

# Platform Checks -------------------------------------------------------------
uname_s=$(uname -s)
uname_m=$(uname -m)

# OS
[ "$uname_s" = "Linux" ] || fatal "This application is only supported on Linux. Detected OS: $uname_s"
# Architecture
([ "$uname_m" = "x86_64" ] || [ "$uname_m" = "amd64" ]) || fatal "This application is only supported on x86_64/amd64. Detected architecture: $uname_m"
# Disallow root
[ "$(id -u)" -ne 0 ] || fatal "Running as root is unsafe. Please run as a non-root user."

SUDO=$(command -v sudo || command -v doas || echo "") # some distros use doas
need_sudo() { [ -n "$SUDO" ] || fatal "need sudo or doas to install packages"; }

get_verilator_version() {
	[ -x "$VER_BIN" ] && "$VER_BIN" --version 2>/dev/null | awk '{print $2}' || printf ''
}
get_installed_tag() {
	v=$(get_verilator_version)
	[ -n "$v" ] && printf 'v%s' "$v" || printf ''
}

# Exit if target version already installed
installed_version=$(get_installed_tag)
[ "$installed_version" = "$version" ] && printSuccess "Verilator $version already installed" && exit 0

# Install dependencies --------------------------------------------------------

# helper to install optional packages, ignoring failures
install_optionals() {
	cmd=$1; shift
    printf "Installing optional dependencies: %s\n" "$*"
	for pkg in "$@"; do
		[ "${cmd#nix}" != "$cmd" ] && pkg="nixpkgs.$pkg"
		$cmd "$pkg" || printWarn "warning: failed to install optional package '$pkg'; continuing"
	done
	printSuccess "Optional dependencies processed"
    printf "\n"
}

install_deps_apt() {
	need_sudo
	CORE="git help2man perl python3 make g++ autoconf flex bison perl-doc libfl-dev zlib1g-dev"
	OPT="ccache mold libgoogle-perftools-dev numactl"
	printf "Installing dependencies (APT): %s\n" "$CORE"
	$SUDO apt-get update -yq || fatal "failed to update apt repositories"
	$SUDO apt-get install -yq $CORE || fatal "failed to install dependencies"
	printSuccess "Dependencies installed"; printf "\n"
	install_optionals "$SUDO apt-get install -yq" $OPT
}

# Nix/NixOS: install build tools into the *user profile* (no root, no system mutation)
# Uses legacy nix-env (no flakes required) for portability.
install_deps_nix() {
	CORE="git help2man perl python3 gnumake gcc autoconf flex bison zlib"
	OPT="ccache mold gperftools numactl"
	printf "Installing dependencies (Nix user profile): %s\n" "$CORE"
	nix-env -iA $(printf 'nixpkgs.%s ' $CORE) || fatal "failed to install dependencies"
	printSuccess "Dependencies installed"; printf "\n"
	install_optionals "nix-env -iA" $OPT
}

install_deps_dnf() {
	need_sudo
	CORE="git help2man perl python3 make gcc-c++ autoconf flex bison perl-Data-Dumper zlib-devel"
	OPT="ccache mold gperftools-devel numactl"
	printf "Installing dependencies (DNF): %s\n" "$CORE"
	$SUDO dnf install -y $CORE || fatal "failed to install dependencies via dnf"
	printSuccess "Dependencies installed"; printf "\n"
	install_optionals "$SUDO dnf install -y" $OPT
}

install_deps_pacman() {
	need_sudo
	CORE="git help2man perl python make gcc autoconf flex bison zlib"
	OPT="ccache mold gperftools numactl"
	printf "Installing dependencies (pacman): %s\n" "$CORE"
	$SUDO pacman -Sy --needed --noconfirm $CORE || fatal "failed to install dependencies"
	printSuccess "Dependencies installed"; printf "\n"
	install_optionals "$SUDO pacman -S --needed --noconfirm" $OPT
}

install_deps_zypper() {
	need_sudo
	CORE="git help2man perl python3 make gcc-c++ autoconf flex bison zlib-devel"
	OPT="ccache mold gperftools-devel numactl"
	printf "Installing dependencies (zypper): %s\n" "$CORE"
	$SUDO zypper --non-interactive install $CORE || fatal "failed to install dependencies"
	printSuccess "Dependencies installed"; printf "\n"
	install_optionals "$SUDO zypper --non-interactive install" $OPT
}

install_deps_xbps() {
	need_sudo
	# NOTE: package names are the Void defaults (glibc/musl variants both ok).
	CORE="git help2man perl python3 make gcc autoconf flex bison zlib-devel"
	OPT="ccache mold gperftools-devel numactl"
	printf "Installing dependencies (xbps): %s\n" "$CORE"
	$SUDO xbps-install -S || fatal "failed to sync XBPS repositories"
	$SUDO xbps-install -y $CORE || fatal "failed to install dependencies via xbps-install"
	printSuccess "Dependencies installed"; printf "\n"
	install_optionals "$SUDO xbps-install -y" $OPT
}

install_deps_apk() {
	need_sudo
	# build-base = gcc g++ make musl-dev; keeps names stable across Alpine versions
	CORE="git help2man perl python3 autoconf flex bison zlib-dev build-base"
	OPT="ccache mold gperftools-dev numactl"
	printf "Installing dependencies (apk): %s\n" "$CORE"
	$SUDO apk add --no-cache $CORE || fatal "failed to install dependencies"
	printSuccess "Dependencies installed"; printf "\n"
	install_optionals "$SUDO apk add --no-cache" $OPT
}

install_deps() {
	if command -v apt-get >/dev/null 2>&1; then install_deps_apt; return 0; fi
	if command -v nix-env >/dev/null 2>&1; then install_deps_nix; return 0; fi
	if command -v dnf >/dev/null 2>&1; then install_deps_dnf; return 0; fi
	if command -v pacman >/dev/null 2>&1; then install_deps_pacman; return 0; fi
	if command -v zypper >/dev/null 2>&1; then install_deps_zypper; return 0; fi
	if command -v xbps-install >/dev/null 2>&1; then install_deps_xbps; return 0; fi
	if command -v apk >/dev/null 2>&1; then install_deps_apk; return 0; fi
	fatal "No supported package manager found on this system. Supported: apt, nix, dnf, pacman, zypper, xbps, apk"
}

install_deps

# Pre-Build -------------------------------------------------------------------
# Pick threads based on CPUs and RAM. Conservative to avoid swapping.
# Rule of thumb: ~2 GiB per parallel C++ compile; reserve 1 GiB for OS.

# CPUs (portable detection)
cpu_max=$(
	getconf _NPROCESSORS_ONLN 2>/dev/null ||
		nproc 2>/dev/null ||
		sysctl -n hw.ncpu 2>/dev/null ||
		printf 1
)

# Total RAM (kB) — Linux via /proc; macOS/bsd via sysctl; fallback 2 GiB
if [ -r /proc/meminfo ]; then
	mem_kb=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null)
else
	mem_bytes=$(sysctl -n hw.memsize 2>/dev/null || printf $((2 * 1024 * 1024 * 1024)))
	mem_kb=$((mem_bytes / 1024))
fi
[ -n "${mem_kb:-}" ] || mem_kb=$((2 * 1024 * 1024)) # 2 GiB fallback

RESERVE_MB=1024     # 1 GiB reserved for system
MEM_PER_JOB_MB=2048 # ~2 GiB per parallel job

avail_mb=$((mem_kb / 1024 - RESERVE_MB))
[ "$avail_mb" -gt 0 ] || avail_mb=512

mem_threads=$((avail_mb / MEM_PER_JOB_MB))
[ "$mem_threads" -ge 1 ] || mem_threads=1

auto_threads=$mem_threads
[ "$cpu_max" -lt "$auto_threads" ] && auto_threads=$cpu_max

# Select THREADS: 0 = auto; else clamp to [1, cpu_max]
if [ "${threads:-0}" -eq 0 ]; then
	THREADS=$auto_threads
	note="auto-detected"
else
	THREADS=$threads
	note="user-specified"
fi

# Clamp and warn if needed
if [ "$THREADS" -lt 1 ]; then
	printf "threads=%s < 1; clamping to 1\n" "$THREADS" >&2
	THREADS=1
elif [ "$THREADS" -gt "$cpu_max" ]; then
	printf "threads=%s > CPUs=%s; clamping to %s\n" "$THREADS" "$cpu_max" "$cpu_max" >&2
	THREADS=$cpu_max
fi

printf "Using %s thread(s) (%s). CPUs=%s, RAM≈%s MiB, suggested=%s\n\n" \
	"$THREADS" "$note" "$cpu_max" "$((mem_kb / 1024))" "$auto_threads"

# Obtain / Validate Repo ------------------------------------------------------
[ -d "$VER_ROOT_DIR/.git" ] || git clone "$VER_REPO" "$VER_ROOT_DIR" || fatal "git clone failed for $VER_REPO"

(
	cd "$VER_ROOT_DIR" || fatal "cd $VER_ROOT_DIR failed"
    # Ensure one Ctrl-C kills build and spinner from *this* subshell
    trap '
    # stop spinner first so it can’t redraw
    [ -n "${_spin_pid:-}" ] && kill "${_spin_pid}" 2>/dev/null;
    # clear current line and restore cursor (in case spinner hid it)
    printf "\r\033[2K" 1>&2
    _show_cursor
    if [ -n "${_cmd_pid:-}" ]; then
        # kill command process group if we created one; else the pid
        kill -TERM -"${_cmd_pid}" 2>/dev/null || kill -TERM "${_cmd_pid}" 2>/dev/null
    fi
    wait 2>/dev/null
    exit 130
    ' INT TERM HUP QUIT

    # Minimal POSIX spinner wrapper
	# Usage: run_with_spinner "Message..." <cmd> [args...]
	# Streams command output to $BUILD_LOG (default: ./build.log)
	run_with_spinner() {
		msg=$1; shift
		: "${BUILD_LOG:=./build.log}"
		printf "%s " "$msg"
        # Append a header for this step
        {
          printf '\n===== %s =====\n' "$msg"
          date -u +"%Y-%m-%dT%H:%M:%SZ"
        } >>"$BUILD_LOG" 2>/dev/null

		# Run the command in background and log output
        if command -v setsid >/dev/null 2>&1; then
        # new session/process-group so we can kill the whole tree with kill -PGID
        setsid "$@" >>"$BUILD_LOG" 2>&1 &
        else
        # fallback (still kill the pid on abort; may miss grandchildren)
        "$@" >>"$BUILD_LOG" 2>&1 &
        fi
        _cmd_pid=$!

		# Only animate if stdout is a TTY
        if [ -t 1 ]; then
            _hide_cursor

            # Background subshell that animates; its traps are local
            (
                # ignore signals; parent trap will handle abort/cleanup
                trap '' INT TERM HUP

                # Choose frames (safe for UTF-8)
                case "${LC_ALL:-${LC_CTYPE:-$LANG}}" in
                *UTF-8*|*utf8*)  set -- ⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏ ;;
                *)               set -- - '\' '|' / ;;
                esac

                if sleep 0.1 2>/dev/null; then
                    SPINNER_DELAY=0.1
                else
                    SPINNER_DELAY=1
                fi

                # Spin until the build command exits
                while kill -0 "$_cmd_pid" 2>/dev/null; do
                frame=$1
                printf "\r%s %s" "$msg" "$frame"
                shift
                set -- "$@" "$frame"   # rotate frames
                sleep "$SPINNER_DELAY"
                done

                # Clear spinner line once the command exits
                printf "\r\033[2K"
            ) &
            _spin_pid=$!
        fi

		# Wait for the command to finish
		wait "$_cmd_pid"
		status=$?

		# Stop spinner if it’s running
		if [ -n "${_spin_pid:-}" ]; then
			kill "$_spin_pid" 2>/dev/null
			wait "$_spin_pid" 2>/dev/null
			_show_cursor
		fi

		if [ $status -eq 0 ]; then
			printSuccess "$msg — done"
		else
			printWarn "$msg — failed (see $BUILD_LOG)"
			tail -n 40 "$BUILD_LOG" 2>/dev/null
			fatal "$msg failed"
		fi
	}

	# Checkout target version -------------------------------------------------
	# Update refs/tags
	git fetch --tags --prune --quiet origin || fatal "git fetch failed"
	git reset --hard --quiet || fatal "git reset failed"
	# Resolve 'latest' to the newest semver-ish tag
	if [ "$version" = "latest" ]; then
		version=$(git tag --sort=v:refname | tail -n1) || fatal "failed to resolve latest tag"
		[ -n "$version" ] || fatal "no tags found in repository"
	fi
	# Checkout the requested version in detached HEAD (no local branch)
	git -c advice.detachedHead=false checkout -q --detach "tags/$version" || fatal "git checkout $version failed"

	printSuccess "Checked out Verilator $version"

	# Auto Configure ----------------------------------------------------------
	# set build envs before configure in case they affect it

	# ccache / mold if available
	if command -v ccache >/dev/null 2>&1; then
		export CXX="ccache g++"
		export CCACHE_CPP2=1
		export CCACHE_SLOPPINESS=time_macros
	else
		export CXX=g++
	fi
    
    CXXFLAGS_EXTRA=""
    LDFLAGS_EXTRA=""
    if command -v mold >/dev/null 2>&1; then
        CXXFLAGS_EXTRA="$CXXFLAGS_EXTRA -fuse-ld=mold"
        LDFLAGS_EXTRA="$LDFLAGS_EXTRA -fuse-ld=mold"
    fi

	export MAKEFLAGS="-j$THREADS"
    BASE_CXXFLAGS="-O2 -g1 -march=native -mtune=native -pipe -fno-omit-frame-pointer -fno-strict-aliasing"
    export CXXFLAGS="${CXXFLAGS:-} ${BASE_CXXFLAGS} ${CXXFLAGS_EXTRA}"
    export LDFLAGS="${LDFLAGS:-} ${LDFLAGS_EXTRA}"

	run_with_spinner "Bootstrapping (autoconf)" autoconf
	run_with_spinner "Configuring" ./configure

	# Build --------------------------------------------------------------------
	# Quiet build, but keep a full log for debugging if it fails
	run_with_spinner "Building (this may take several minutes)" make

	# Verify install ----------------------------------------------------------
	[ -x "$VER_BIN" ] || fatal "Build succeeded but $VER_BIN not found"
	installed_version=$(get_installed_tag)
	[ -n "$installed_version" ] || fatal "failed to get installed version from $VER_BIN"
	printSuccess "Verilator $version installed successfully"
	[ "$installed_version" = "$version" ] || fatal "Version mismatch after build: expected '$version', got '$installed_version'"
)
