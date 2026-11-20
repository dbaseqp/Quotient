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
# What browser_curl writes to stdout is capped at BROWSER_CURL_MAX_BYTES
# (default 10 MiB, the web check's page limit): past it the output is cut
# there and browser_curl exits 23, curl's write error. Files written with -o
# are not capped. Otherwise browser_curl exits with curl's status.
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

# curl's options that take a value: short ones (also last in a cluster such as
# -sH, or with the value attached as in -HAccept:x) and long ones.
_bc_short_values=ACDEFHKPQTUXYbcdehmortuwxyz
_bc_long_values=' abstract-unix-socket alt-svc aws-sigv4 cacert capath cert cert-type ciphers config connect-timeout connect-to continue-at cookie cookie-jar create-file-mode crlfile curves data data-ascii data-binary data-raw data-urlencode delegation dns-interface dns-ipv4-addr dns-ipv6-addr dns-servers doh-url dump-header ech egd-file engine etag-compare etag-save expect100-timeout form form-string ftp-account ftp-alternative-to-user ftp-method ftp-port ftp-ssl-ccc-mode happy-eyeballs-timeout-ms header help hostpubmd5 hostpubsha256 hsts interface ip-tos ipfs-gateway json keepalive-time key key-type krb libcurl limit-rate local-port login-options mail-auth mail-from mail-rcpt max-filesize max-redirs max-time netrc-file noproxy oauth2-bearer output output-dir parallel-max pass pinnedpubkey preproxy proto proto-default proto-redir proxy proxy-cacert proxy-capath proxy-cert proxy-cert-type proxy-ciphers proxy-crlfile proxy-header proxy-key proxy-key-type proxy-pass proxy-pinnedpubkey proxy-service-name proxy-tls13-ciphers proxy-tlsauthtype proxy-tlspassword proxy-tlsuser proxy-user pubkey quote random-file range rate referer request request-target resolve retry retry-delay retry-max-time sasl-authzid service-name sigalgs socks4 socks4a socks5 socks5-gssapi-service socks5-hostname speed-limit speed-time ssl-sessions stderr telnet-option tftp-blksize time-cond tls-max tls13-ciphers tlsauthtype tlspassword tlsuser trace trace-ascii trace-config unix-socket upload-file upload-flags url url-query user user-agent variable vlan-priority write-out '

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

# _bc_url notes whether the request's first http(s) URL is a trustworthy origin.
_bc_url() {
	[ -z "$_bc_trusted" ] || return 0
	case $1 in
	https://* | http://localhost | http://localhost[:/]* | http://127.* | http://\[::1\]*) _bc_trusted=yes ;;
	http://*) _bc_trusted=no ;;
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

	# Keep one byte past the cap to tell a body of exactly the cap from a
	# longer one, whose curl may have finished before head stopped reading.
	_bc_max=${BROWSER_CURL_MAX_BYTES:-10485760}
	_bc_tmp=$(mktemp -d) || return
	{
		command curl "$@"
		echo $? >"$_bc_tmp/status"
	} | head -c "$((_bc_max + 1))" >"$_bc_tmp/out"
	_bc_status=$(cat "$_bc_tmp/status")
	if [ "$(($(wc -c <"$_bc_tmp/out")))" -gt "$_bc_max" ]; then
		head -c "$_bc_max" "$_bc_tmp/out"
		_bc_status=23
	else
		cat "$_bc_tmp/out"
	fi
	rm -rf "$_bc_tmp"
	return "$_bc_status"
}
