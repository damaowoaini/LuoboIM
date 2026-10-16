"""Task-only HTTPS admin integration. No production target or credential output."""
from pathlib import Path
import base64, hashlib, hmac, json, os, secrets, ssl, struct, time, sys
import urllib.parse, urllib.request, urllib.error

PROJECT = Path(__file__).resolve().parents[2]
PRIVATE = PROJECT.parent / "references/integration/stack-config"
STATE = PRIVATE / "parity-normal-admin.json"
PUBLIC = PROJECT / "docs/architecture/admin-permissions-live.json"
ORIGIN = "https://admin.luobo.fixture.test"
tls = PROJECT.parent / "references/integration/tls"
opener = urllib.request.build_opener(
    urllib.request.ProxyHandler({"https": "http://127.0.0.1:38081"}),
    urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(tls / "ca.pem"))),
)
results = []

def private_save(value):
    with os.fdopen(os.open(STATE, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600), "w") as f:
        json.dump(value, f)

def call(label, path, body, token=None):
    headers = {"Content-Type": "application/json", "operationID": secrets.token_hex(16)}
    if token:
        headers["token"] = token
    start = time.monotonic()
    req = urllib.request.Request(ORIGIN + path, json.dumps(body).encode(), headers, method="POST")
    try:
        response = opener.open(req, timeout=20)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        value = json.load(response)
        results.append({"case": label, "path": path, "httpStatus": response.status,
            "errCode": value.get("errCode"), "elapsedSeconds": round(time.monotonic()-start, 3)})
    return value

def ok(label, path, body, token=None):
    value = call(label, path, body, token)
    if value.get("errCode") != 0:
        raise RuntimeError(f"{label}: errCode {value.get('errCode')}")
    return value.get("data") or {}

def totp(secret):
    key = base64.b32decode(secret + "=" * (-len(secret) % 8), casefold=True)
    digest = hmac.new(key, struct.pack(">Q", int(time.time()) // 30), hashlib.sha1).digest()
    index = digest[-1] & 15
    return f"{(struct.unpack('>I', digest[index:index+4])[0] & 0x7fffffff) % 1000000:06d}"

resume = STATE.exists() and sys.argv[1:] == ["--resume-readonly"]
if STATE.exists() and not resume:
    raise SystemExit("Private checkpoint exists; inspect its phase instead of repeating mutations.")
super_session = json.loads((PRIVATE / "parity-admin-session.json").read_text())["login"]
super_token = super_session["adminToken"]
if resume:
    state = json.loads(STATE.read_text())
    assert state["phase"] in ["bound", "ready_for_UI"]
    results = json.loads(PUBLIC.read_text())["results"]
else:
    password = secrets.token_urlsafe(24)
    state = {"account": "luobo-qa-" + secrets.token_hex(6), "password": password,
        "passwordWire": hashlib.md5(password.encode()).hexdigest(), "phase": "before_create"}
    private_save(state)
try:
    if not resume:
        ok("superadmin_create_disposable_normal", "/complete_admin/account/add_admin",
           {"account": state["account"], "password": state["passwordWire"], "nickname": "独立管理验收"}, super_token)
        state["phase"] = "created"; private_save(state)
        first = ok("unenrolled_login_no_tokens", "/complete_admin/account/login",
            {"account": state["account"], "password": state["passwordWire"]})
        assert not first.get("adminToken") and not first.get("imToken")
        state["userID"] = first["adminUserID"]
        qr = ok("normal_MFA_enrollment", "/complete_admin/google_auth_manager/qrcode",
            {"account": state["account"], "password": state["passwordWire"]})
        state["totp_secret"] = urllib.parse.parse_qs(urllib.parse.urlparse(qr["qrCodeUrl"]).query)["secret"][0]
        state["phase"] = "before_bind"; private_save(state)
        ok("normal_MFA_bind", "/complete_admin/google_auth_manager/bind",
            {"account": state["account"], "password": state["passwordWire"], "code": totp(state["totp_secret"])})
        state["phase"] = "bound"; private_save(state)
    state["login"] = ok("normal_password_TOTP_login", "/complete_admin/account/login",
        {"account": state["account"], "password": state["passwordWire"], "googleAuthCode": totp(state["totp_secret"])})
    assert state["login"]["level"] == 80 and state["login"]["adminToken"] and state["login"]["imToken"]
    private_save(state)
    normal = state["login"]["adminToken"]
    for label, path, body in [
        ("normal_admin_list_denied", "/complete_admin/account/list", {"pagination": {"pageNumber":1,"showNumber":20}}),
        ("normal_own_enable_still_requires_super", "/complete_admin/account/set_status", {"userID":state["userID"],"enabled":True}),
        ("normal_privilege_escalation_denied", "/complete_admin/account/update", {"level":100}),
        ("normal_new_preview_scope_denied", "/complete_admin/admin/message_preview/conversations", {"userID":json.loads((PRIVATE / "root-business-accounts.json").read_text())["users"][0]["userID"],"pageNumber":1,"pageSize":20}),
    ]:
        response = call(label,path,body,normal)
        assert response.get("errCode") == 1002, label
    ok("normal_own_profile", "/complete_admin/account/info", {}, normal)
    # Existing IM admin API is still shipped in the UI. Check the synthetic
    # conversation only; retain no message content or credentials in public output.
    users = json.loads((PRIVATE / "root-business-accounts.json").read_text())["users"]
    old = call("normal_legacy_message_search_scope", "/api/msg/search_msg",
        {"sendID":users[1]["userID"],"recvID":users[0]["userID"],"sessionType":1,
         "msgType":101,"sendTime":"","pagination":{"pageNumber":1,"showNumber":10}}, state["login"]["imToken"])
    content = (old.get("data") or {}).get("chatLogs") or []
    results[-1]["returnedMessageCount"] = len(content)
    results[-1]["syntheticPlaintextVisible"] = any("luobo-web-once-" in str(row.get("content","")) for row in content)
    state["phase"] = "ready_for_UI"; private_save(state)
finally:
    PUBLIC.write_text(json.dumps({"schema_version":1,"date":time.strftime("%Y-%m-%dT%H:%M:%S"),
        "transport":"actual task CA+hostname HTTPS and exact CONNECT; real password/TOTP; no synthetic JWT",
        "scope":"one disposable Level80 admin only; A/B service configuration and existing business accounts unchanged",
        "results":results,"private_state":"protected checkpoint; no credentials in public evidence",
        "cleanup":"keep disposable actor for UI tests; delete it through superadmin normal API after completion"},ensure_ascii=False,indent=2)+"\n")
print(json.dumps({"phase":state["phase"],"cases":len(results),"codes":[r["errCode"] for r in results]}))
