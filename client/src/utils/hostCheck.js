// Fork addition: the server's answer to a host name check (GET /api/dns and
// /api/dns-check) in plain words. The server passes on Go's resolver error
// ("Internal server error: lookup x on 127.0.0.11:53: server misbehaving");
// this says what it means and what to do. Each result is [key, English,
// values] for t(). Pure, tested by hostCheck.test.mjs.

// A .local name is answered by the devices of the network themselves
// (multicast DNS), not by a DNS server: looking it up from the server says
// nothing about whether it works (components/localNames.jsx says that).
export const skipLookup = (host) => /\.local\.?$/i.test(String(host || '').trim().split(':')[0]);

const strip = (message) => String(message || '')
  .replace(/^(Internal server error|DNS Check error)\s*:\s*/i, '')
  .trim();

// host: the name the admin typed; message: the server's error text
export const hostCheckText = (host, message) => {
  const name = String(host || '').trim().split(':')[0].slice(0, 253);
  const raw = strip(message);
  const values = { name };
  if (/no such host|NXDOMAIN|not found|no DNS entry/i.test(raw)) {
    return ['hostCheck.noSuchName',
      '{{name}} does not exist in DNS yet. Create the record at your DNS provider (pointing to this server), or use a local address. The app can be installed now; the address works once the name exists.', values];
  }
  if (/server misbehaving|i\/o timeout|connection refused|network is unreachable|no route to host|temporary failure|timed out|SERVFAIL/i.test(raw)) {
    return ['hostCheck.noResolver',
      'Cosmos could not look up {{name}}: this server cannot reach a DNS server right now. Check the server\'s network and DNS settings (/etc/resolv.conf). The app can be installed now; the address works once the name resolves.', values];
  }
  if (/no URL requested/i.test(raw) || !name) {
    return ['hostCheck.empty', 'Enter a host name to check it.', values];
  }
  return ['hostCheck.other', 'Cosmos could not check {{name}} ({{reason}}). The app can be installed anyway.', { name, reason: raw.slice(0, 160) || 'no answer' }];
};
