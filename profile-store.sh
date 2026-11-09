#!/usr/bin/env bash
# Prints aggregate stats about an lg store: counts, sizes, compression ratios.
# Read-only on the store; prints no file names or contents.
# Needs: lg, zstd, find, awk, tar; unzip for the zip sample. Optional: passwordless sudo (sudo -n) or root, to drop the page cache for a cold read test.
# Run it as the user that owns the lg daemon, so `lg root` finds the daemon's store.
# Usage: profile-store.sh [sample_attempts]   (default 200)
set -euo pipefail
N=${1:-200}
root=$(lg root)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
echo "== store: $(du -sh "$root" | cut -f1) on disk, $(du -sh --apparent-size "$root" 2>/dev/null | cut -f1) apparent"
echo "== filesystem: $(df -T "$root" | awk 'NR==2{print $2}')"
echo "== files by kind (count, total bytes, median, p90)"
find "$root" -type f -not -path '*/extracted/*' -printf '%f %s\n' | awk '
  { k=$1; if (k ~ /^log\.txt$/) k="log.txt"; else if (k ~ /\.json$/) k="*.json"; else if (k=="artifact.zip") k="artifact.zip"; else k="other" }
  { print k, $2 }' > "$tmp/sizes"
find "$root" -type f -path '*/extracted/*' -printf 'extracted/* %s\n' >> "$tmp/sizes"
for k in log.txt '*.json' artifact.zip 'extracted/*' other; do
  awk -v k="$k" '$1==k{print $2}' "$tmp/sizes" | sort -n | awk -v k="$k" '
    {a[NR]=$1; s+=$1} END{ if(NR) printf "  %-12s n=%d bytes=%.2fGB median=%dKB p90=%dKB\n", k, NR, s/1e9, a[int(NR/2)+1]/1024, a[int(NR*0.9)+1]/1024 }'
done
echo "== runs per day (last 14 days): $(find "$root" -mindepth 5 -maxdepth 5 -type d -path '*/runs/*' | sort | tail -14 | while read -r d; do find "$d" -mindepth 1 -maxdepth 1 -type d | wc -l; done | tr '\n' ' ')"
echo "== jobs per attempt (median): $(find "$root" -type d -name 'attempt-*' | head -2000 | while read -r a; do find "$a/jobs" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l; done | sort -n | awk '{a[NR]=$1} END{print a[int(NR/2)+1]}')"
echo "== compression of log.txt in $N random attempts"
find "$root" -type d -name 'attempt-*' | shuf -n "$N" > "$tmp/attempts"
: > "$tmp/logs"; while read -r a; do find "$a" -name log.txt >> "$tmp/logs"; done < "$tmp/attempts"
raw=$(xargs -d '\n' cat < "$tmp/logs" | wc -c)
[ "$raw" -gt 0 ] || { echo "  no logs sampled"; exit 0; }
r() { awk -v a="$raw" -v b="$1" 'BEGIN{printf "%.1fx", a/b}'; }
for l in 3 19; do
  s=0; while read -r f; do s=$((s + $(zstd -q -$l -c "$f" | wc -c))); done < "$tmp/logs"; echo "  per-file zstd -$l: $(r $s)"
done
half=$(( $(wc -l < "$tmp/logs") / 2 ))
head -n "$half" "$tmp/logs" > "$tmp/train"; tail -n +"$((half+1))" "$tmp/logs" > "$tmp/test"
mapfile -t train < "$tmp/train"
if zstd -q --train "${train[@]}" --maxdict=112640 -o "$tmp/dict" 2>/dev/null; then
  t=$(xargs -d '\n' cat < "$tmp/test" | wc -c); s=0
  while read -r f; do s=$((s + $(zstd -q -3 -D "$tmp/dict" -c "$f" | wc -c))); done < "$tmp/test"
  echo "  per-file zstd -3 with dictionary: $(awk -v a="$t" -v b="$s" 'BEGIN{printf "%.1fx", a/b}')"
fi
s=0; while read -r a; do s=$((s + $(find "$a" -name log.txt -exec cat {} + | zstd -q -3 -c | wc -c))); done < "$tmp/attempts"
echo "  per-attempt bundle zstd -3: $(r $s)"
find "$root" -name artifact.zip | shuf -n 50 > "$tmp/zips"
if [ -s "$tmp/zips" ] && command -v unzip > /dev/null; then
  z=0; u=0; c=0
  while read -r f; do
    z=$((z + $(stat -c %s "$f"))); u=$((u + $(unzip -p "$f" | wc -c))); c=$((c + $(unzip -p "$f" | zstd -q -3 -T0 --long=27 -c | wc -c)))
  done < "$tmp/zips"
  echo "== 50 artifact.zip: zip ${z}B, unzipped ${u}B, zstd -3 of contents ${c}B ($(awk -v a="$z" -v b="$c" 'BEGIN{printf "%.1fx smaller than zip", a/b}'))"
fi
# extracted/ holds nested archives expanded too, so it shows what a fully expanded tar.zst would cost.
find "$root" -type d -name extracted -prune | shuf -n 50 > "$tmp/dirs"
if [ -s "$tmp/dirs" ]; then
  z=0; u=0; c=0; nested=0
  while read -r d; do
    [ -f "$d/../artifact.zip" ] || continue
    z=$((z + $(stat -c %s "$d/../artifact.zip"))); u=$((u + $(du -sb --apparent-size "$d" | cut -f1)))
    c=$((c + $(tar -C "$d" -cf - . | zstd -q -3 -T0 --long=27 -c | wc -c)))
    nested=$((nested + $(find "$d" -type d -name '*.d' | wc -l)))
  done < "$tmp/dirs"
  if [ "$z" -gt 0 ]; then echo "== 50 extracted/ trees: zip ${z}B, expanded ${u}B, nested archives ${nested}, tar.zst -3 ${c}B ($(awk -v a="$z" -v b="$c" 'BEGIN{printf "%.1fx smaller than zip", a/b}'))"; fi
fi
# Dropping the page cache needs root. Run as the daemon's user, this asks sudo without a password prompt.
drop_caches() {
  sync
  if [ "$(id -u)" = 0 ]; then echo 3 > /proc/sys/vm/drop_caches
  else echo 3 | sudo -n tee /proc/sys/vm/drop_caches > /dev/null
  fi
}
if drop_caches 2> /dev/null; then
  echo "== cold read of the sampled logs, one at a time"
  start=$(date +%s.%N); xargs -d '\n' cat < "$tmp/logs" > /dev/null; end=$(date +%s.%N)
  awk -v n="$(wc -l < "$tmp/logs")" -v b="$raw" -v t="$(echo "$end - $start" | bc)" 'BEGIN{printf "  %d files, %.0fMB in %.1fs: %.1f MB/s, %.1f ms/file\n", n, b/1e6, t, b/1e6/t, t*1000/n}'
else
  echo "== cold read test skipped (needs root or passwordless sudo to drop the page cache)"
fi
