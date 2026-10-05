#!/bin/sh
# Bump the static-asset revision: copies the canonical css/js to rev-suffixed
# FILE names (timerpi.vNN.js …) and re-points every reference (templates, JS
# import specifiers inside the rev'd copies, Go micro-pages). Rev'd names are
# brand-new URL paths, so middleboxes that portal-cache by path — ignoring
# Cache-Control and ?v= — cannot serve stale bytes. Older rev'd files are
# kept in public/ so long-lived cached pages never break.
#   usage: tools/bump-assets.sh vNN     (run after the final JS/CSS edits)
set -e
REV="$1"
case "$REV" in v[0-9]*) ;; *) echo "usage: $0 vNN"; exit 2;; esac

for f in timerpi board mesh engine undo theme waiting; do
  cp "public/src/$f.js" "public/src/$f.$REV.js"
done
cp public/css/timerpi.css "public/css/timerpi.$REV.css"

# Templates: accept both older shapes (dir-rev /src/vN/..., ?v= tokens) and
# canonical names, and land everything on the rev'd file name.
sed -E -i "s#/src/v[0-9]+/(timerpi|board|mesh|engine)(\.v[0-9]+)?\.js#/src/\1.$REV.js#g; s#/css/v[0-9]+/timerpi(\.v[0-9]+)?\.css#/css/timerpi.$REV.css#g; s#/src/(timerpi|board|mesh|engine)\.js\?v=[a-z0-9]*#/src/\1.$REV.js#g; s#/css/timerpi\.css\?v=[a-z0-9]*#/css/timerpi.$REV.css#g; s#/src/(timerpi|board|mesh|engine)\.v[0-9]+\.js#/src/\1.$REV.js#g; s#/css/timerpi\.v[0-9]+\.css#/css/timerpi.$REV.css#g" \
  templates/base.html templates/display.html templates/display_board.html templates/settings.html

# The rev'd JS copies point at the rev'd sibling imports (undo + theme ride
# the timerpi/board imports, so their specifiers must be retargeted too).
for f2 in timerpi board mesh engine; do
  sed -E -i "s#\./(mesh|engine|board|undo|theme|waiting)\.js#\./\1.$REV.js#g" "public/src/$f2.$REV.js"
done

# Go micro-pages (login / show lock) ride the derived Go const references.
for go_file in routes/auth.go routes/showauth.go; do
  sed -E -i "s#timerpi(\.v[0-9]+)?\.css#timerpi.$REV.css#g; s#assetsRev = \"v[0-9]+\"#assetsRev = \"$REV\"#" "$go_file"
done
# Prune: keep the 3 newest revs per asset (a cached page can only be a few
# revs behind); canonical names always stay.
python3 - <<'PYEOF'
import glob, os, re
names = {}
for p in glob.glob('public/src/*.v*.js') + glob.glob('public/css/*.v*.css'):
    m = re.search(r'[.][v](\d+)[.](?:js|css)$', p)
    if m:
        names.setdefault(p.rsplit('.', 2)[0], []).append((int(m.group(1)), p))
for base, revs in names.items():
    revs.sort()
    for _, p in revs[:-3]:
        os.remove(p)
PYEOF
echo "bumped to $REV"
