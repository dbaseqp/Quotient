# browser-curl.sh: source this from a shell check so curl sends what desktop
# Chrome sends to load a page, instead of curl's own
# "User-Agent: curl/8.x" and "Accept: */*" (which no browser sends):
#
#   . /app/lib/browser-curl.sh
#   body=$(browser_curl -s "http://$ADDRESS/")
#
# browser_curl passes all of its arguments to curl unchanged and adds Chrome's
# navigation headers in Chrome's HTTP/1.1 order. Client hints, Sec-Fetch-* and
# br/zstd are only sent to https and loopback URLs, as Chrome does. A header
# the check sets itself (-H 'Accept: ...', -H 'Accept;', -A) replaces Chrome's;
# with a Referer (-e or -H) Sec-Fetch-Site and Sec-Fetch-User are left out.
# Responses are decompressed (--compressed), so a regex sees plain text.
#
# browser_curl prints at most the first BROWSER_CURL_MAX_BYTES (default
# 10 MiB) of the body, like the web check's prefix, and exits 0 when it cut
# the body there, so a regex scores on that prefix. Files written with -o are
# not cut. Otherwise browser_curl exits with curl's status.
#
# Limits: on https, curl's TLS ClientHello is OpenSSL's, not Chrome's, so a
# TLS (JA3/JA4) fingerprint still shows curl. When an https server offers
# HTTP/2, curl negotiates it and its SETTINGS, window updates and
# pseudo-header order are curl's, not Chrome's. Headers curl adds itself
# (Authorization for -u, Content-Type and Content-Length for -d) keep curl's
# positions.

# The Quotient runner picks the Chrome each check run claims (ChromeEnv).
_bc_missing='browser-curl.sh: the Quotient runner sets QUOTIENT_CHROME_USER_AGENT, QUOTIENT_CHROME_PLATFORM, QUOTIENT_CHROME_SEC_CH_UA and QUOTIENT_CHROME_LANGUAGE for each check; set them to run a check by hand'
: "${QUOTIENT_CHROME_USER_AGENT:?$_bc_missing}" "${QUOTIENT_CHROME_PLATFORM:?$_bc_missing}" \
	"${QUOTIENT_CHROME_SEC_CH_UA:?$_bc_missing}" "${QUOTIENT_CHROME_LANGUAGE:?$_bc_missing}"

# Offer br and zstd to trustworthy origins only if this curl can decode them.
case $(command curl --version) in
*Features:*brotli*zstd*) _bc_trusted_encodings='gzip, deflate, br, zstd' ;;
*) _bc_trusted_encodings='gzip, deflate' ;;
esac

# curl's options that take a value (those --help all shows with an <arg>, or
# [arg] in older curl): short ones (also last in a cluster such as -sH, or
# with the value attached as in -HAccept:x) and long ones.
_bc_help=$(command curl --help all)
_bc_short_values=$(printf '%s\n' "$_bc_help" | sed -n 's/^ *-\(.\), --[^ ]* [<[].*/\1/p' | tr -d '\n')
_bc_long_values=$(printf '%s\n' "$_bc_help" | sed -n 's/^ *\(-., \)\{0,1\}--\([^ ]*\) [<[].*/\2/p' | tr '\n' ' ')
_bc_no_help='browser-curl.sh: curl --help all lists no options that take a value, so browser_curl cannot tell option values from URLs'
: "${_bc_short_values:?$_bc_no_help}" "${_bc_long_values:?$_bc_no_help}"
_bc_long_values=" $_bc_long_values"

# _bc_option_value records what the value $2 of option $1 says about the
# request: a header the check sets, or (--url) the URL.
_bc_option_value() {
	case $1 in
	-H | --header) _bc_own="$_bc_own$(printf '%s' "${2%%[;:]*}" | tr 'A-Z' 'a-z') " ;;
	-A | --user-agent) _bc_own="$_bc_own""user-agent " ;;
	-e | --referer) _bc_own="$_bc_own""referer " ;;
	--url) _bc_url "$2" ;;
	esac
}

# _bc_url notes whether the request's first http(s) URL is a potentially
# trustworthy origin as Chrome counts them: https, localhost, *.localhost,
# 127.0.0.0/8 and [::1].
_bc_url() {
	[ -z "$_bc_trusted" ] || return 0
	case $1 in
	https://*) _bc_trusted=yes ;;
	http://*)
		_bc_host=${1#http://}
		_bc_host=${_bc_host%%[/?#]*}
		_bc_host=${_bc_host##*@}
		case $_bc_host in \[*\]) ;; *:*) _bc_host=${_bc_host%:*} ;; esac
		case $_bc_host in
		localhost | *.localhost | \[::1\]) _bc_trusted=yes ;;
		*[!0-9.]* | *..* | *. | *.*.*.*.*) _bc_trusted=no ;;
		127.*.*.*) _bc_trusted=yes ;;
		*) _bc_trusted=no ;;
		esac ;;
	esac
}

# _bc_sets reports whether the check sets header $1 (lower case) itself.
_bc_sets() {
	case $_bc_own in *" $1 "*) return 0 ;; esac
	return 1
}

browser_curl() {
	_bc_own=' ' _bc_trusted='' _bc_option=''
	for _bc_arg in "$@"; do
		if [ -n "$_bc_option" ]; then
			_bc_option_value "$_bc_option" "$_bc_arg"
			_bc_option=''
			continue
		fi
		case $_bc_arg in
		--?*)
			case $_bc_long_values in *" ${_bc_arg#--} "*) _bc_option=$_bc_arg ;; esac ;;
		-?*)
			_bc_rest=${_bc_arg#-}
			while [ -n "$_bc_rest" ]; do
				_bc_letter=${_bc_rest%"${_bc_rest#?}"}
				_bc_rest=${_bc_rest#?}
				case $_bc_short_values in *"$_bc_letter"*)
					if [ -n "$_bc_rest" ]; then
						_bc_option_value "-$_bc_letter" "$_bc_rest"
					else
						_bc_option=-$_bc_letter
					fi
					break ;;
				esac
			done ;;
		*) _bc_url "$_bc_arg" ;;
		esac
	done

	# Append Chrome's headers, in Chrome's order and without those the check
	# sets, after the check's arguments, then rotate the check's arguments to
	# the end: curl sends -H headers in the order given.
	_bc_n=$#
	_bc_sets connection || set -- "$@" -H 'Connection: keep-alive'
	if [ "$_bc_trusted" = yes ]; then
		_bc_sets sec-ch-ua || set -- "$@" -H "sec-ch-ua: $QUOTIENT_CHROME_SEC_CH_UA"
		_bc_sets sec-ch-ua-mobile || set -- "$@" -H 'sec-ch-ua-mobile: ?0'
		_bc_sets sec-ch-ua-platform || set -- "$@" -H "sec-ch-ua-platform: $QUOTIENT_CHROME_PLATFORM"
	fi
	_bc_sets upgrade-insecure-requests || set -- "$@" -H 'Upgrade-Insecure-Requests: 1'
	_bc_sets user-agent || set -- "$@" -H "User-Agent: $QUOTIENT_CHROME_USER_AGENT"
	_bc_sets accept || set -- "$@" -H 'Accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7'
	_bc_encodings='gzip, deflate'
	if [ "$_bc_trusted" = yes ]; then
		# A Referer means the check claims a link it followed; which site
		# relation that is, only the check knows.
		_bc_sets referer || _bc_sets sec-fetch-site || set -- "$@" -H 'Sec-Fetch-Site: none'
		_bc_sets sec-fetch-mode || set -- "$@" -H 'Sec-Fetch-Mode: navigate'
		_bc_sets referer || _bc_sets sec-fetch-user || set -- "$@" -H 'Sec-Fetch-User: ?1'
		_bc_sets sec-fetch-dest || set -- "$@" -H 'Sec-Fetch-Dest: document'
		_bc_encodings=$_bc_trusted_encodings
	fi
	_bc_sets accept-encoding || set -- "$@" -H "Accept-Encoding: $_bc_encodings"
	_bc_sets accept-language || set -- "$@" -H "Accept-Language: $QUOTIENT_CHROME_LANGUAGE"
	set -- "$@" --compressed
	while [ "$_bc_n" -gt 0 ]; do
		set -- "$@" "$1"
		shift
		_bc_n=$((_bc_n - 1))
	done

	# dd copies the first $_bc_max bytes to stdout (fd 4). It reads exactly
	# that many from the pipe, where busybox head -c reads ahead, so a byte
	# left in the pipe means the body was longer; head then exits and stops
	# curl. curl's status and "over" come back on fd 3, without temp files.
	_bc_max=${BROWSER_CURL_MAX_BYTES:-10485760}
	{
		_bc_status=$(
			{
				{ command curl "$@" 3>&- 4>&- && echo 0 >&3 || echo $? >&3; } |
					{
						dd bs=65536 iflag=fullblock,count_bytes count="$_bc_max" status=none >&4 3>&- 4>&-
						if [ "$(($(head -c 1 | wc -c)))" -gt 0 ]; then echo over >&3; fi
					}
			} 3>&1
		)
	} 4>&1
	case $_bc_status in *over*) return 0 ;; esac
	return "$_bc_status"
}
