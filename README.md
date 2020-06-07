# proxyprober

Finds the largest header size that can be passed. This might reveal intermediate
proxy servers that add extra headers.

## Usage

Either use `go build` first, or run directly with `go run proxyprober.go ...`.

Try to send a request padded to 32768 bytes (currently elicits a 431 error):

    ./proxyprober -url https://example.com -max-size 65536

Automatically probe for the maximum (the result varies below 63kiB):

    ./proxyprober -url https://example.com -detect -min-size 32768 -max-size 65536

## Automatic detection methodology

Assume a upper bound for the header field length and the request header size. To
probe these parameters, pad the request header with `X-Pad`, `X-Pad1`, etc.
header fields up to the maximum header field length.

Consider a request to "fail by size" if it one of the following status codes:

 - 400 Bad Request (RFC 7231, Section 6.5.1)
 - 414 URI Too Long (RFC 7231, Section 6.5.12)
 - 431 Request Header Fields Too Large (RFC 6585, Section 5)

Long header fields, a large number of header fields, or a large header size
could trigger a 400 or 431 error. However, assuming that a parser bails out as
early as possible, it is assumed that a large request line (including method and
target) will trigger a 414 error before other conditions are detected.

Follow these steps to probe for the size:

 1. Perform an initial probe with the request header padded to the assumed lower
    bound. If the request already fails by size, abort the probe.
 2. Perform a binary search between the lower and upper bounds.
    - Consider a "fail by size" signal as hint to reduce the upper bound.
    - If the request code matches the initial probe, the lower bound can be
      raised.
    - Consider a server error such as 502 Bad Gateway as hint to retry a probe.
    - Other error codes require further investigation, so fail with no
      definitive answer.

Note that due to implementation-specific buffering behavior, the actual header
size limit could be slightly higher. Additionally, if the maximum request field
size is incorrect, it could result in a too low detected header size limit. Due
to additional (debug) headers or diverse load balancers, the detected limit can
also vary.

## Servers

Various servers have limits on their maximum header field length as well as the
total size of all headers combined. The header field includes the header name,
the colon, any spaces, and the CRLF. HTTP/1.1 and HTTP/2 may differ in behavior.

 * nginx: limits HTTP/1.1 header fields to 8096 bytes and the total header size
   to four times that number. See
   [`large_client_header_buffers`](https://nginx.org/r/large_client_header_buffers).
   Up to (by default) 1024 bytes can be added to that number, the initial full
   header fields within that limit will not trigger a buffer allocation. See
   [`client_header_buffer_size`](https://nginx.org/r/client_header_buffer_size).
