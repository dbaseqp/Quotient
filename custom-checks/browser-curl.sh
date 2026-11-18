# browser-curl.sh: source this from a shell check so curl sends what desktop
# Chrome sends to load a page, instead of curl's own
# "User-Agent: curl/8.x" and "Accept: */*" (which no browser sends):
#
#   . /app/checks/browser-curl.sh
#   body=$(browser_curl -s "http://$ADDRESS/")
#
# browser_curl passes all of its arguments to curl unchanged and adds Chrome's
# navigation headers in Chrome's HTTP/1.1 order. Client hints, Sec-Fetch-* and
# br/zstd are only sent to https and loopback URLs, as Chrome does. A header
# the check sets itself (-H 'Accept: ...', -A, -e) replaces Chrome's.
# Responses are decompressed (--compressed), so a regex sees plain text as
# before.
#
# Limits: on https, curl's TLS ClientHello is OpenSSL's, not Chrome's, so a
# TLS (JA3/JA4) fingerprint still shows curl. Headers curl adds itself
# (Authorization for -u, Content-Type and Content-Length for -d) keep curl's
# positions.

# The Chrome release claimed and its sec-ch-ua come from the runner
# (checks.ChromeMajor, the one place the version is kept).
BROWSER_CURL_CHROME=${QUOTIENT_CHROME_MAJOR:?browser-curl.sh: QUOTIENT_CHROME_MAJOR is set by the Quotient runner}
BROWSER_CURL_SEC_CH_UA=${QUOTIENT_CHROME_SEC_CH_UA:?browser-curl.sh: QUOTIENT_CHROME_SEC_CH_UA is set by the Quotient runner}
BROWSER_CURL_ACCEPT='text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7'

# One browser per check run: the platform is picked once, when sourced.
case $(($(od -An -tu2 -N2 /dev/urandom) % 3)) in
0) BROWSER_CURL_OS='Windows NT 10.0; Win64; x64' BROWSER_CURL_PLATFORM='"Windows"' ;;
1) BROWSER_CURL_OS='Macintosh; Intel Mac OS X 10_15_7' BROWSER_CURL_PLATFORM='"macOS"' ;;
*) BROWSER_CURL_OS='X11; Linux x86_64' BROWSER_CURL_PLATFORM='"Linux"' ;;
esac
BROWSER_CURL_UA="Mozilla/5.0 ($BROWSER_CURL_OS) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/$BROWSER_CURL_CHROME.0.0.0 Safari/537.36"
case $(od -An -tu1 -N1 /dev/urandom | tr -d ' ') in
*[0-4]) BROWSER_CURL_LANGUAGE='en-US,en;q=0.9' ;;
*[5-6]) BROWSER_CURL_LANGUAGE='en-US,en;q=0.9,es;q=0.8' ;;
*7) BROWSER_CURL_LANGUAGE='en-GB,en-US;q=0.9,en;q=0.8' ;;
*) BROWSER_CURL_LANGUAGE='en-US,en;q=0.9,fr;q=0.8' ;;
esac

browser_curl() {
	# What the check sets itself, and whether the URL is a trustworthy origin.
	_bc_own=' ' _bc_trusted='' _bc_prev=''
	for _bc_arg in "$@"; do
		case $_bc_prev in
		-H | --header) _bc_own="$_bc_own$(printf '%s' "${_bc_arg%%:*}" | tr 'A-Z' 'a-z') " ;;
		-A | --user-agent) _bc_own="$_bc_own""user-agent " ;;
		-e | --referer) _bc_own="$_bc_own""referer " ;;
		esac
		case $_bc_arg in
		-A?* | --user-agent=*) _bc_own="$_bc_own""user-agent " ;;
		-H?*) _bc_own="$_bc_own$(printf '%s' "${_bc_arg#-H}" | sed 's/:.*//' | tr 'A-Z' 'a-z') " ;;
		https://* | http://localhost[:/]* | http://localhost | http://127.* | http://\[::1\]*)
			[ -n "$_bc_trusted" ] || _bc_trusted=yes ;;
		http://*) [ -n "$_bc_trusted" ] || _bc_trusted=no ;;
		esac
		_bc_prev=$_bc_arg
	done

	# Chrome's headers, in Chrome's order, one per line; skip those the check set.
	_bc_list=''
	_bc_add() {
		case $_bc_own in *" $1 "*) return ;; esac
		_bc_list="$_bc_list$2
"
	}
	_bc_add connection 'Connection: keep-alive'
	if [ "$_bc_trusted" = yes ]; then
		_bc_add sec-ch-ua "sec-ch-ua: $BROWSER_CURL_SEC_CH_UA"
		_bc_add sec-ch-ua-mobile 'sec-ch-ua-mobile: ?0'
		_bc_add sec-ch-ua-platform "sec-ch-ua-platform: $BROWSER_CURL_PLATFORM"
	fi
	_bc_add upgrade-insecure-requests 'Upgrade-Insecure-Requests: 1'
	_bc_add user-agent "User-Agent: $BROWSER_CURL_UA"
	_bc_add accept "Accept: $BROWSER_CURL_ACCEPT"
	_bc_encodings='gzip, deflate'
	if [ "$_bc_trusted" = yes ]; then
		_bc_add sec-fetch-site 'Sec-Fetch-Site: none'
		_bc_add sec-fetch-mode 'Sec-Fetch-Mode: navigate'
		_bc_add sec-fetch-user 'Sec-Fetch-User: ?1'
		_bc_add sec-fetch-dest 'Sec-Fetch-Dest: document'
		# Offer br and zstd only if this curl can decode them.
		_bc_features=$(curl --version | grep '^Features:')
		case $_bc_features in *brotli*) case $_bc_features in *zstd*) _bc_encodings='gzip, deflate, br, zstd' ;; esac ;; esac
	fi
	_bc_add accept-encoding "Accept-Encoding: $_bc_encodings"
	_bc_add accept-language "Accept-Language: $BROWSER_CURL_LANGUAGE"

	# Append the headers after the check's arguments, then rotate the check's
	# arguments to the end: curl sends -H headers in the order given.
	_bc_n=$#
	while IFS= read -r _bc_h; do
		[ -n "$_bc_h" ] && set -- "$@" -H "$_bc_h"
	done <<EOF
$_bc_list
EOF
	set -- "$@" --compressed
	while [ "$_bc_n" -gt 0 ]; do
		set -- "$@" "$1"
		shift
		_bc_n=$((_bc_n - 1))
	done
	curl "$@"
}
