"""Read-only authority snapshots for the two task-owned UI CRUD words."""
from pathlib import Path
import json, os, secrets, ssl, sys, urllib.request

root = Path(__file__).resolve().parents[2]
private = root.parent / "references/integration/stack-config"
state_path = private / "parity-sensitive-crud.json"
state = json.loads(state_path.read_text())
actor = json.loads((private / "parity-normal-admin.json").read_text())
context = ssl.create_default_context(cafile=str(root.parent / "references/integration/tls/ca.pem"))
opener = urllib.request.build_opener(urllib.request.ProxyHandler({"https":"http://127.0.0.1:38081"}), urllib.request.HTTPSHandler(context=context))
body = {"keyword":state["prefix"],"pageNumber":1,"showNumber":20}
req = urllib.request.Request("https://admin.luobo.fixture.test/api/sensitive_word/search", json.dumps(body).encode(),
    {"Content-Type":"application/json","operationID":secrets.token_hex(16),"token":actor["login"]["imToken"]}, method="POST")
with opener.open(req, timeout=20) as response:
    data = json.load(response)
assert data.get("errCode") == 0
rows = (data.get("data") or {}).get("words") or []
assert all(row["word"] in state["words"] for row in rows)
stage = sys.argv[1] if len(sys.argv)>1 else "readonly"
snapshot = {"stage":stage,"errCode":0,"total":(data.get("data") or {}).get("total",0),
    "statuses":sorted(row["status"] for row in rows),"exactOwnWordsOnly":True}
state["latestRows"] = rows
state["state"] = stage
state.setdefault("snapshots",[]).append(snapshot)
with os.fdopen(os.open(state_path,os.O_WRONLY|os.O_TRUNC,0o600),"w") as f:
    json.dump(state,f)
evidence = {"schema_version":1,"actor":"disposable actual Level80 admin with password+TOTP",
    "scope":"only two uniquely named synthetic words; existing words and service configuration untouched",
    "UI":"existing admin layout; batch add, individual enable/disable, search, cache refresh and normal confirmed delete under inspection",
    "authoritySnapshots":state["snapshots"],"screenshots":["../verification/web/43-admin-sensitive-created-normal.png"],
    "remaining":"UI deletion and cleanup pending; do not infer all89 API acceptance"}
(root / "docs/architecture/admin-sensitive-crud-live.json").write_text(json.dumps(evidence,ensure_ascii=False,indent=2)+"\n")
print(json.dumps(snapshot))
