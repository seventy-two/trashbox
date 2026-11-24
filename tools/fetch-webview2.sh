#!/bin/sh
# Fetches the WebView2 SDK (headers + static loader) from NuGet into
# deps/webview2. Not in git: it's Microsoft's, and 11 MB.
set -e
cd "$(dirname "$0")/.."
V=$(curl -s https://api.nuget.org/v3-flatcontainer/microsoft.web.webview2/index.json |
    python3 -c "import json,sys;print([x for x in json.load(sys.stdin)['versions'] if '-' not in x][-1])")
tmp=$(mktemp -d)
curl -sL -o "$tmp/wv2.zip" "https://api.nuget.org/v3-flatcontainer/microsoft.web.webview2/$V/microsoft.web.webview2.$V.nupkg"
rm -rf deps/webview2 && mkdir -p deps/webview2/x64
python3 -I -c "
import zipfile,sys,shutil,os
z=zipfile.ZipFile(sys.argv[1])
for n,dst in [('build/native/include/WebView2.h','deps/webview2/include/WebView2.h'),
              ('build/native/include/WebView2EnvironmentOptions.h','deps/webview2/include/WebView2EnvironmentOptions.h'),
              ('build/native/x64/WebView2LoaderStatic.lib','deps/webview2/x64/WebView2LoaderStatic.lib'),
              ('LICENSE.txt','deps/webview2/LICENSE.txt')]:
    os.makedirs(os.path.dirname(dst),exist_ok=True)
    with z.open(n) as s, open(dst,'wb') as d: shutil.copyfileobj(s,d)
" "$tmp/wv2.zip"
rm -rf "$tmp"
echo "WebView2 SDK $V in deps/webview2"
