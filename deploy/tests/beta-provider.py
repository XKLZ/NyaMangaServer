#!/usr/bin/env python3
"""Anonymous CONNECT fixture: never forwards traffic or contacts a real site.

Only mounted into the separately named beta fixture container. Its ephemeral CA
is mounted read-only into the anonymous application, never installed on the host.
"""
import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import ssl
from urllib.parse import urlsplit, parse_qs


class Gallery(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def respond(self, value, media):
        body = value.encode() if isinstance(value, str) else json.dumps(value).encode()
        self.send_response(200)
        self.send_header('Content-Type', media)
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Connection', 'close')
        self.end_headers()
        self.wfile.write(body)
        self.close_connection = True

    def do_GET(self):
        query = parse_qs(urlsplit(self.path).query)
        if self.headers.get('Host') not in ('e-hentai.org', 'exhentai.org') or not query.get('f_shash'):
            self.send_error(404)
            return
        self.respond('<html><a href="https://e-hentai.org/g/900001/abcdef0123/">Anonymous gallery</a></html>', 'text/html; charset=utf-8')

    def do_POST(self):
        if self.headers.get('Host') != 'api.e-hentai.org' or self.path != '/api.php':
            self.send_error(404)
            return
        length = int(self.headers.get('Content-Length', '0'))
        if length < 1 or length > 4096:
            self.send_error(413)
            return
        value = json.loads(self.rfile.read(length))
        if value.get('method') != 'gdata' or len(value.get('gidlist', [])) != 1:
            self.send_error(400)
            return
        gid, token = value['gidlist'][0]
        self.respond({'gmetadata': [{'gid': gid, 'token': token, 'title': 'Anonymous imported gallery', 'title_jpn': '匿名インポート作品', 'category': 'Manga', 'rating': '4.25', 'posted': '1700000000', 'filecount': '3', 'filesize': '12345', 'tags': ['language:japanese', 'parody:beta import', 'artist:beta artist']}]}, 'application/json')


class Proxy(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_CONNECT(self):
        if self.path not in ('e-hentai.org:443', 'exhentai.org:443', 'api.e-hentai.org:443'):
            self.send_error(403)
            return
        self.send_response(200, 'Connection established')
        self.end_headers()
        self.wfile.flush()
        try:
            with self.server.tls.wrap_socket(self.connection, server_side=True) as connection:
                Gallery(connection, self.client_address, self.server)
        except (ssl.SSLError, ConnectionError):
            pass
        self.close_connection = True


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--certificate', required=True)
    parser.add_argument('--key', required=True)
    parser.add_argument('--port', type=int, default=18090)
    parser.add_argument('--bind', default='0.0.0.0')
    args = parser.parse_args()
    server = ThreadingHTTPServer((args.bind, args.port), Proxy)
    server.tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    server.tls.load_cert_chain(args.certificate, args.key)
    server.serve_forever()
