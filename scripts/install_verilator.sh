#!/bin/sh

# Target: POSIX Linux (APT-only) x86_64/amd64, run-in-place verilator install
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

printfSuccess() { printf '\033[32m%s\033[0m\n' "$@"; }
printfWarn() { printf '\033[33m%s\033[0m\n' "$@" >&2; }
printfErr() { printf '\033[31m%s\033[0m\n' "$@" >&2; }
fatal() { printfErr "$@"; exit 1; }

cleanup() { :; }
on_exit() { status=$?; cleanup; return; }

trap 'on_exit' EXIT
trap 'printfErr "Aborted"; exit 130' INT TERM HUP QUIT

# Parse Args ------------------------------------------------------------------
DEFAULT_VERSION="v5.006"
version="$DEFAULT_VERSION"
threads=0  # 0 = auto-detect

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
        -h|--help) usage; exit 0 ;;
        version=*) version=${1#version=}; [ -n "$version" ] || fatal "error: version= must not be empty" ;;
        threads=*) threads=${1#threads=}; [ -n "$threads" ] || fatal "error: threads= must not be empty" ;;
        *=*) key=${1%%=*}; fatal "error: unknown option '$key=...'" ;;
        -*) fatal "error: unknown flag '$1'" ;;
        *) fatal "error: positional args are not supported ('$1')" ;;
    esac
    shift
done

[ "$version" = "latest" ] || printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]{3}$' || fatal "invalid version: $version"

case $threads in
    ''|*[!0-9]*) fatal "error: threads= must be an integer >= 0 (0=auto-detect)" ;;
esac

# Platform Checks -------------------------------------------------------------
uname_s=$(uname -s)
uname_m=$(uname -m)

# OS
[ "$uname_s" = "Linux" ] || fatal "This application is only supported on Linux. Detected OS: $uname_s"
# Architecture
( [ "$uname_m" = "x86_64" ] || [ "$uname_m" = "amd64" ] ) || fatal "This application is only supported on x86_64/amd64. Detected architecture: $uname_m"
# Disallow root
[ "$(id -u)" -ne 0 ] || fatal "Running as root is unsafe. Please run as a non-root user."

get_verilator_version() {
    [ -x "$VER_BIN" ] && "$VER_BIN" --version | awk '{print $2}' || printf ''
}
get_installed_tag() {
    v=$(get_verilator_version)
    [ -n "$v" ] && printf 'v%s' "$v" || printf ''
}

# Exit if target version already installed
installed_version=$(get_installed_tag)
if [ "$installed_version" = "$version" ]; then
    printfSuccess "Verilator $version is already installed at $VER_BIN"
    exit 0
fi

# Temp require APT
command -v apt-get >/dev/null 2>&1 || fatal "Error: at the moment this installer requires an APT-based distribution (Debian/Ubuntu). Wider support will be added in the future."

# Install dependencies --------------------------------------------------------
APT_DEPS="git help2man perl python3 make g++ autoconf flex bison perl-doc libfl-dev zlib1g-dev"
OPTIONAL_APT_DEPS="ccache mold libgoogle-perftools-dev numactl"
printf "Installing dependencies: $APT_DEPS\n"
sudo apt-get update -yq && sudo apt-get install -yq $APT_DEPS || fatal "failed to install dependencies via apt-get"

# Install optionals individually so one miss doesn't block the rest
printf "Installing optional dependencies: %s\n" "$OPTIONAL_APT_DEPS"
for pkg in $OPTIONAL_APT_DEPS; do
  if ! sudo apt-get install -yq "$pkg"; then
    printfWarn "warning: optional package '%s' not installed; continuing" "$pkg"
  fi
done

# Pre-Build -------------------------------------------------------------------
# Pick threads based on CPUs and RAM. Conservative to avoid swapping.
# Rule of thumb: ~2 GiB per parallel C++ compile; reserve 1 GiB for OS.

# CPUs (portable detection)
cpu_max=$(
    getconf _NPROCESSORS_ONLN 2>/dev/null \
    || nproc 2>/dev/null \
    || sysctl -n hw.ncpu 2>/dev/null \
    || printf 1
)

# Total RAM (kB) — Linux via /proc; macOS/bsd via sysctl; fallback 2 GiB
if [ -r /proc/meminfo ]; then
    mem_kb=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null)
else
    mem_bytes=$(sysctl -n hw.memsize 2>/dev/null || printf $((2 * 1024 * 1024 * 1024)))
    mem_kb=$((mem_bytes / 1024))
fi
[ -n "${mem_kb:-}" ] || mem_kb=$((2 * 1024 * 1024))  # 2 GiB fallback

RESERVE_MB=1024          # 1 GiB reserved for system
MEM_PER_JOB_MB=2048      # ~2 GiB per parallel job

avail_mb=$(( mem_kb / 1024 - RESERVE_MB ))
[ "$avail_mb" -gt 0 ] || avail_mb=512

mem_threads=$(( avail_mb / MEM_PER_JOB_MB ))
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

printf "Using %s thread(s) (%s). CPUs=%s, RAM≈%s MiB, suggested=%s\n" \
       "$THREADS" "$note" "$cpu_max" "$((mem_kb/1024))" "$auto_threads"

# Obtain / Validate Repo ------------------------------------------------------
[ -d "$VER_ROOT_DIR/.git" ] || git clone "$VER_REPO" "$VER_ROOT_DIR" || fatal "git clone failed for $VER_REPO"

(
    cd "$VER_ROOT_DIR" || fatal "cd $VER_ROOT_DIR failed"

    # Checkout target version -------------------------------------------------
    # Update refs/tags
    git fetch --tags --prune origin || fatal "git fetch failed"
    git reset --hard || fatal "git reset failed"
    # Resolve 'latest' to the newest semver-ish tag
    if [ "$version" = "latest" ]; then
        version=$(git tag --sort=v:refname | tail -n1) || fatal "failed to resolve latest tag"
        [ -n "$version" ] || fatal "no tags found in repository"
    fi
    # Checkout the requested version in detached HEAD (no local branch)
    git -c advice.detachedHead=false checkout --detach "tags/$version" || fatal "git checkout $version failed"

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
    if command -v mold >/dev/null 2>&1; then
        export LD="$(command -v mold)"
    fi
    
    export MAKEFLAGS="-j$THREADS"
    export CXXFLAGS="-O2 -g1 -march=native -mtune=native -pipe -fno-omit-frame-pointer -fno-strict-aliasing"
    export LDFLAGS="${LDFLAGS:-}"

    autoconf || fatal "autoconf failed"
    ./configure || fatal "configure failed"

    # Build --------------------------------------------------------------------
    # Quiet build, but keep a full log for debugging if it fails
    printf "Building... Full log: $BUILD_LOG\n"
    if ! make >"$BUILD_LOG" 2>&1; then
        fatal "Build failed. See: $BUILD_LOG for details.\n"
    fi

    # Verify install ----------------------------------------------------------
    [ -x "$VER_BIN" ] || fatal "Build succeeded but $VER_BIN not found"
    installed_version=$(get_installed_tag)
    [ -n "$installed_version" ] || fatal "failed to get installed version from $VER_BIN"
    printfSuccess "Verilator $version installed successfully"
    [ "$installed_version" = "$version" ] || fatal "Version mismatch after build: expected '$version', got '$installed_version'"
)
