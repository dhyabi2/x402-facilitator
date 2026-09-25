#!/usr/bin/env python3
# Fetch a recent confirmed SEND block from Nano RPC for a live-test fixture.
# Prints only public chain data. Diagnoses auth failures.
import json, os, urllib.request, urllib.error

def load_env(p):
    d = {}
    with open(os.path.expanduser(p)) as f:
        for line in f:
            line = line.strip()
            if line and not line.startswith('#') and '=' in line:
                k, v = line.split('=', 1)
                d[k] = v
    return d

env = load_env('~/.hermes/.env')
url = env.get('NANO_RPC_URL', 'https://rpc.nano.to')

def rpc(body, with_key=True):
    req = urllib.request.Request(url, data=json.dumps(body).encode(),
                                 headers={'Content-Type': 'application/json'})
    if with_key:
        req.add_header('x-api-key', env.get('NANO_RPC_KEY', ''))
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        return {"__httperr": e.code, "__body": e.read()[:300].decode('utf-8', 'replace')}

# verify the configured key works with a simple read; print nothing secret
v = rpc({"action": "version"})
print("version call:", json.dumps(v)[:200])

account = "nano_3t6k35gi95xu6tergt6p69ck76ogmaysaimes3v5aqo4u8nkoqe4wg9hfw6w"
d = rpc({"action": "account_info", "account": account})
print("account_info:", json.dumps(d)[:300])

d = rpc({"action": "account_history", "account": account, "count": -30})
hist = d.get('history', [])
print("history rows:", len(hist), "err:", d.get('__httperr', d.get('error')))
for h in hist[:30]:
    if h.get('type') == 'send':
        print("SEND", h.get('hash'), "amount_raw", h.get('amount'),
              "link_as_account", h.get('link_as_account'), "account", h.get('account'))
