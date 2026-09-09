import ctypes, json, logging, logging.handlers, os, sys, threading, time, urllib.error, urllib.request
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from abstraction_model import family as parsed_family

try:
    import abstraction_job as job
except ImportError:
    sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "job", "python"))
    import abstraction_job as job

try:
    PDH = ctypes.WinDLL("pdh.dll")
except (OSError, AttributeError):
    PDH = None

DWORD, HANDLE = ctypes.c_ulong, ctypes.c_void_p

ROCM = os.environ.get("RESIDENT_ROCM_BIN") or os.path.expanduser(
    r"~\.cache\lemonade\bin\therock\gfx1151-7.13.0\bin")
LOG_DIR = os.environ.get("RESIDENT_LOG_DIR") or os.path.expanduser(r"~\.abstraction\logs")
MERGES_FILE = os.environ.get("RESIDENT_MERGES") or os.path.expanduser(r"~\.abstraction\model-merges")
DOWN_BACKOFF = float(os.environ.get("RESIDENT_BACKOFF", "10"))
JOBS = os.environ.get("ABSTRACTION_STORE") or os.path.expanduser("~/.abstraction")
KEEPER_TTL = float(os.environ.get("RESIDENT_KEEPER_TTL", "10"))
RECALL_GRACE = float(os.environ.get("RESIDENT_RECALL_GRACE", "30"))
KIND = "resident"

log = logging.getLogger("resident")

STORE = None


def clock():
    return datetime.now(timezone.utc)


def store():
    global STORE
    if STORE is None:
        STORE = job.FileStore(JOBS)
    return STORE

MERGES = {}


def read_merges(path=None):
    """Two hosts spell one model so differently that the parser splits them, and
    the parser cannot be taught every name that will ever exist. A line
    `wrong-key canonical-key` says the person disagreed, without a code change."""
    out = {}
    try:
        with open(path or MERGES_FILE, encoding="utf-8") as f:
            for line in f:
                line = line.split("#", 1)[0].split()
                if len(line) == 2:
                    out[line[0]] = line[1]
    except OSError:
        pass
    return out


def family(s):
    f = parsed_family(s)
    for _ in range(len(MERGES) + 1):
        nxt = MERGES.get(f, f)
        if nxt == f:
            return f
        f = nxt
    return f


def start_logging():
    os.makedirs(LOG_DIR, exist_ok=True)
    fmt = logging.Formatter("%(asctime)s %(message)s")
    rotating = logging.handlers.RotatingFileHandler(
        os.path.join(LOG_DIR, "resident.log"), maxBytes=1 << 20, backupCount=3, encoding="utf-8")
    rotating.setFormatter(fmt)
    log.addHandler(rotating)
    if sys.stdout is not None:
        log.addHandler(logging.StreamHandler(sys.stdout))
    log.setLevel(logging.INFO)


_HIP = []


def device_memory():
    try:
        if not _HIP:
            os.add_dll_directory(ROCM)
            _HIP.append(ctypes.CDLL(os.path.join(ROCM, "amdhip64_7.dll")))
        if _HIP[0] is None:
            return None, None
        free, total = ctypes.c_size_t(), ctypes.c_size_t()
        _HIP[0].hipMemGetInfo(ctypes.byref(free), ctypes.byref(total))
        return round((total.value - free.value) / 2**30, 2), round(total.value / 2**30, 2)
    except (OSError, AttributeError):
        _HIP[:] = [None]
        return None, None


def http(method, url, body=None, timeout=600):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.status, r.read()


def get_json(url, timeout=4):
    try:
        return json.loads(http("GET", url, timeout=timeout)[1])
    except (OSError, ValueError):
        return None


def first_per_family(names):
    """One name per family. A host offers several builds of one model —
    llama.cpp, MTP, an NPU recipe — and they are one family, so something has to
    choose which to ask for. A build already resident wins, because loading a
    second build of a model the host already holds is the waste this exists to
    stop; otherwise the host's own order decides."""
    out = {}
    for n in names:
        out.setdefault(family(n), n)
    return out


class Host:
    def __init__(self, name, base):
        self.name, self.base = name, base
        self.down_until = 0.0

    def read(self):
        if time.time() < self.down_until:
            return {"up": False, "reason": "not answering, retrying in %.0fs"
                                           % (self.down_until - time.time())}
        try:
            cat = self.catalogue()
            if cat is None:
                raise OSError("no answer on " + self.base)
            res = self.resident()
        except Exception as e:
            self.sideline()
            return {"up": False, "reason": "%s: %s" % (type(e).__name__, e)}
        self.down_until = 0.0
        return {"up": True, "resident": res,
                "resident_families": sorted({family(m) for m in res}),
                "catalogue": first_per_family(res + cat)}

    def sideline(self):
        self.down_until = time.time() + DOWN_BACKOFF


class Lemonade(Host):
    def catalogue(self):
        d = get_json(self.base + "/api/v1/models")
        if d is None:
            return None
        return [m["id"] for m in d.get("data", []) if m.get("downloaded")]

    def resident(self):
        h = get_json(self.base + "/api/v1/health")
        if not h:
            return []
        loaded = [m["model_name"] for m in (h.get("all_models_loaded") or []) if m.get("loaded")]
        one = h.get("model_loaded")
        if one and one not in loaded:
            loaded.append(one)
        return loaded

    def load(self, model):
        http("POST", self.base + "/api/v1/load", {"model_name": model}, timeout=900)

    def unload(self, model):
        http("POST", self.base + "/api/v1/unload", {"model_name": model}, timeout=120)

    def chat_url(self):
        return self.base + "/api/v1/chat/completions"


class LMStudio(Host):
    def catalogue(self):
        d = get_json(self.base + "/api/v0/models")
        if d is None:
            return None
        return [m["id"] for m in d.get("data", []) if m.get("type") in ("llm", "vlm")]

    def resident(self):
        d = get_json(self.base + "/api/v0/models") or {}
        return [m["id"] for m in d.get("data", []) if m.get("state") == "loaded"]

    def load(self, model):
        http("POST", self.chat_url(),
             {"model": model, "messages": [{"role": "user", "content": "hi"}], "max_tokens": 1},
             timeout=900)

    def unload(self, model):
        """LM Studio unloads by instance, and one model key can be loaded as
        several instances, so every instance of the key goes."""
        listed = get_json(self.base + "/api/v1/models") or {}
        instances = [i["id"] for m in listed.get("models", []) if m.get("key") == model
                     for i in m.get("loaded_instances", [])] or [model]
        for instance in instances:
            http("POST", self.base + "/api/v1/models/unload", {"instance_id": instance}, timeout=120)

    def chat_url(self):
        return self.base + "/v1/chat/completions"


class Ollama(Host):
    def catalogue(self):
        d = get_json(self.base + "/api/tags")
        if d is None:
            return None
        return [m["name"] for m in d.get("models", [])]

    def resident(self):
        d = get_json(self.base + "/api/ps") or {}
        return [m["name"] for m in d.get("models", [])]

    def load(self, model):
        http("POST", self.base + "/api/generate", {"model": model, "keep_alive": "30m"}, timeout=900)

    def unload(self, model):
        http("POST", self.base + "/api/generate", {"model": model, "keep_alive": 0}, timeout=120)

    def chat_url(self):
        return self.base + "/v1/chat/completions"


HOSTS = [Lemonade("lemonade", "http://127.0.0.1:13305"),
         LMStudio("lmstudio", "http://127.0.0.1:1234"),
         Ollama("ollama", "http://127.0.0.1:11434")]

STATE = {"hosts": {}, "decisions": [], "gpu_holders": [], "gpu_readable": False, "evicted": []}
LOCK = threading.Lock()


def survey():
    global MERGES
    MERGES = read_merges()
    out = {h.name: h.read() for h in HOSTS}
    used, total = device_memory()
    with LOCK:
        STATE["hosts"] = out
        STATE["merges"] = dict(MERGES)
        STATE["device_used_gib"], STATE["device_total_gib"] = used, total
        STATE["surveyed_at"] = time.time()
    return out


def cached():
    with LOCK:
        return dict(STATE["hosts"])


GPU_COUNTERS = (r"\GPU Process Memory(*)\Local Usage",
                r"\GPU Process Memory(*)\Dedicated Usage")
PID_COUNTER = r"\Process(*)\ID Process"
PDH_FMT_LARGE = 0x400
PDH_MORE_DATA = 0x800007D2


class CounterValue(ctypes.Union):
    _fields_ = [("long", ctypes.c_long), ("double", ctypes.c_double),
                ("large", ctypes.c_longlong), ("string", ctypes.c_wchar_p)]


class CounterItem(ctypes.Structure):
    _fields_ = [("name", ctypes.c_wchar_p), ("status", DWORD),
                ("value", CounterValue)]


def counter_array(counter):
    size, count = DWORD(0), DWORD(0)
    rc = PDH.PdhGetFormattedCounterArrayW(counter, PDH_FMT_LARGE, ctypes.byref(size),
                                          ctypes.byref(count), None)
    if rc & 0xFFFFFFFF != PDH_MORE_DATA:
        return []
    buf = ctypes.create_string_buffer(size.value)
    if PDH.PdhGetFormattedCounterArrayW(counter, PDH_FMT_LARGE, ctypes.byref(size),
                                        ctypes.byref(count), buf):
        return []
    items = ctypes.cast(buf, ctypes.POINTER(CounterItem))
    return [(items[i].name, items[i].value.large) for i in range(count.value)]


def read_counters(paths):
    if PDH is None:
        return None
    query = HANDLE()
    if PDH.PdhOpenQueryW(None, 0, ctypes.byref(query)):
        return None
    try:
        counters = []
        for path in paths:
            c = HANDLE()
            if PDH.PdhAddEnglishCounterW(query, path, 0, ctypes.byref(c)) == 0:
                counters.append(c)
        if not counters or PDH.PdhCollectQueryData(query):
            return None
        out = []
        for c in counters:
            out += counter_array(c)
        return out
    finally:
        PDH.PdhCloseQuery(query)


def gpu_holders():
    """Every GPU runtime on this machine reports memory per-process, and so says
    the GPU is empty while another process holds 22 GB of it. The Windows
    performance counters are the only instrument that sees the whole machine,
    and they name a process only by pid, so a second counter supplies the names —
    reading the process table directly needs a handle the owner's own desktop
    processes refuse."""
    counters = read_counters(GPU_COUNTERS)
    if counters is None:
        return None
    named = {pid: name for name, pid in (read_counters([PID_COUNTER]) or [])}
    by_process = {}
    for instance, value in counters:
        pid = instance.partition("pid_")[2].partition("_")[0]
        if pid.isdigit():
            name = named.get(int(pid), "pid" + pid)
            by_process[name] = by_process.get(name, 0) + value
    return sorted(({"process": n, "gib": round(v / 2**30, 2)}
                   for n, v in by_process.items() if v > 500 * 2**20),
                  key=lambda r: -r["gib"])


def poller(stop=None):
    n = 0
    while stop is None or not stop.is_set():
        try:
            survey()
            reconcile()
            if n % 3 == 0:
                held = gpu_holders()
                with LOCK:
                    STATE["gpu_readable"] = held is not None
                    STATE["gpu_holders"] = held or []
                    STATE["gpu_held_gib"] = round(sum(x["gib"] for x in (held or [])), 2)
        except Exception as e:
            note("poll-error", error="%s: %s" % (type(e).__name__, e))
        n += 1
        time.sleep(2)


def note(kind, **kw):
    kw.update(kind=kind, at=time.strftime("%H:%M:%S"))
    with LOCK:
        STATE["decisions"] = ([kw] + STATE["decisions"])[:50]
    log.info(json.dumps(kw))


LOADING = {}


def family_lock(want):
    with LOCK:
        return LOADING.setdefault(want, threading.Lock())


def pick(hosts, want, skip=()):
    """The first host holding the family, in configured order — never a copy
    that was recalled and did not yield. That is the fallback this broker owns:
    it cannot free another host's memory, but it can stop feeding the copy, and
    every host has an idle timeout that finishes the job."""
    with LOCK:
        evicted = set(STATE["evicted"])
    for h in HOSTS:
        s = hosts.get(h.name, {})
        if h.name in skip or not s.get("up"):
            continue
        for m in s.get("resident", []):
            if family(m) == want and copy_name(h.name, m) not in evicted:
                return h, m
    return None, None


def place(want, skip=()):
    h, m = pick(cached(), want, skip)
    if h:
        note("reuse", family=want, host=h.name, model=m)
        return h, m
    with family_lock(want):
        hosts = survey()
        h, m = pick(hosts, want, skip)
        if h:
            note("reuse", family=want, host=h.name, model=m)
            return h, m
        for c in HOSTS:
            s = hosts.get(c.name, {})
            if c.name in skip or not s.get("up") or want not in s.get("catalogue", {}):
                continue
            target = s["catalogue"][want]
            note("load", family=want, host=c.name, model=target)
            try:
                c.load(target)
            except Exception as e:
                c.sideline()
                note("load-failed", family=want, host=c.name,
                     error="%s: %s" % (type(e).__name__, e))
                continue
            survey()
            return c, target
    return None, None


def copy_name(host, model):
    return "%s/%s" % (host, model)


def keeper(host):
    return "resident@" + host


def host_named(name):
    return next(h for h in HOSTS if h.name == name)


def latest_jobs(st):
    """The newest job per resident copy. `list` is oldest first, so the last
    one seen for a copy is the one that speaks for it."""
    out = {}
    for r in st.list():
        if r.kind == KIND:
            out[(r.spec.get("host", ""), r.spec.get("model", ""))] = r
    return out


def hold(st, key, r=None):
    name, model = key
    if r is None:
        rid = st.submit(job.Record(id="", kind=KIND,
                                   spec={"host": name, "model": model, "family": family(model)}))
        r = st.load(rid)
    return st.claim(r.id, keeper(name), KEEPER_TTL)


def finish(st, key, r, state):
    now = clock()
    if not r.lease.held(now):
        r = st.claim(r.id, keeper(key[0]), KEEPER_TTL)

    def mutate(rec):
        rec.state = state

    st.update(r.id, r.lease.epoch, mutate)


def honour(st, key, r):
    """The keeper's half of a recall: unload, then close the job with the recall
    still on it, so a reader can see it was asked and it yielded."""
    name, model = key
    try:
        host_named(name).unload(model)
    except Exception as e:
        note("recall-refused", host=name, family=family(model),
             error="%s: %s" % (type(e).__name__, e))
        return
    finish(st, key, r, job.COMPLETE)
    note("yield", host=name, family=family(model), reason=r.lease.recall.reason)


def reconcile(hosts=None):
    """After every survey. Every resident copy is a running job held by this
    broker on that host's behalf, so a third party can read who holds what and
    recall it through the store. A recall on a job the broker holds is honoured
    by unloading; one it could not honour lapses into an eviction, which the
    router respects until the copy leaves on its own."""
    hosts = cached() if hosts is None else hosts
    st = store()
    now = clock()
    present = {(n, m) for n, s in hosts.items() if s.get("up") for m in s.get("resident", [])}
    latest = latest_jobs(st)
    evicted = set()
    for key in sorted(present):
        r = latest.get(key)
        try:
            if r is None or r.terminal():
                r = hold(st, key)
            elif not r.lease.held(now):
                if r.lease.recalled():
                    evicted.add(copy_name(*key))
                    continue
                r = hold(st, key, r)
            else:
                r = st.renew(r.id, r.lease.epoch, KEEPER_TTL)
            if r.lease.recalled():
                honour(st, key, r)
        except job.JobError as e:
            note("keeper-error", host=key[0], family=family(key[1]),
                 error="%s: %s" % (type(e).__name__, e))
    for key, r in latest.items():
        if key not in present and not r.terminal():
            try:
                finish(st, key, r, job.COMPLETE)
            except job.JobError as e:
                note("keeper-error", host=key[0], family=family(key[1]),
                     error="%s: %s" % (type(e).__name__, e))
    with LOCK:
        STATE["evicted"] = sorted(evicted)
    arbitrate(st, present)


def arbitrate(st, present):
    """Two copies of one family: keep the one routing prefers, recall the rest.
    The keeper honours it on the next pass, or the lease lapses at the deadline
    and the copy is evicted from routing."""
    order = {h.name: i for i, h in enumerate(HOSTS)}
    by_family = {}
    for key in present:
        by_family.setdefault(family(key[1]), []).append(key)
    now = clock()
    latest = latest_jobs(st)
    for fam, copies in by_family.items():
        copies.sort(key=lambda k: order.get(k[0], len(order)))
        for key in copies[1:]:
            r = latest.get(key)
            if r is None or r.terminal() or r.lease.recalled() or not r.lease.held(now):
                continue
            try:
                st.recall(r.id, r.lease.epoch, "doubled: %s holds %s" % (copies[0][0], fam),
                          "resident-broker", RECALL_GRACE)
                note("recall", host=key[0], family=fam, keep=copies[0][0])
            except job.JobError as e:
                note("recall-failed", host=key[0], family=fam,
                     error="%s: %s" % (type(e).__name__, e))


def copy_status(latest, key, now):
    """What the job that speaks for an extra copy says about it."""
    r = latest.get(key)
    if r is None or not r.lease.recalled():
        return "not yet asked"
    if r.terminal():
        return "yielded"
    if r.lease.held(now):
        return "asked, %.0fs to yield" % (r.lease.recall.until - now).total_seconds()
    return "evicted, not routed to"


def panel(key, title, lede, rows, absence, because, blind=()):
    f = {"key": key, "title": title, "lede": lede, "source": "abstraction",
         "rows": rows, "blind": list(blind)}
    if not rows:
        f["absence"], f["because"] = absence, because
    return f


def findings():
    t0 = time.time()
    with LOCK:
        hosts, held = dict(STATE["hosts"]), list(STATE["gpu_holders"])
        readable, cap = STATE["gpu_readable"], STATE.get("device_total_gib")
    down = sorted(n for n, s in hosts.items() if not s.get("up"))
    unreachable = [{"what": n, "absence": "denied", "because": hosts[n].get("reason", ""),
                    "remedy": "Start %s; the broker notices it within %.0fs without restarting."
                              % (n, DOWN_BACKOFF)} for n in down]
    holders = {}
    for h in HOSTS:
        for m in hosts.get(h.name, {}).get("resident", []):
            holders.setdefault(family(m), {}).setdefault(h.name, m)
    doubled = {f: hs for f, hs in holders.items() if len(hs) > 1}
    try:
        latest = latest_jobs(store())
    except (job.JobError, OSError):
        latest = {}
    now = clock()
    out = [
        panel("hosts", "Model hosts answering on loopback",
              "Every runtime the broker can route an application to.",
              [{"host": n, "models_installed": len(s["catalogue"])}
               for n, s in sorted(hosts.items()) if s.get("up")],
              "nothing matched",
              "None of %s is answering. Nothing can be routed until one starts."
              % (", ".join(sorted(hosts)) or "the configured hosts"),
              unreachable),
        panel("residency", "Models resident right now",
              "One copy, shared by every application that asks for it.",
              [{"host": n, "model": m, "family": family(m)}
               for n, s in sorted(hosts.items()) for m in s.get("resident", [])],
              "nothing matched",
              "No host that answered is holding a model.",
              unreachable + [{"what": "ComfyUI", "absence": "never persisted",
                              "because": "ComfyUI holds GPU memory and exposes no interface naming what is in it.",
                              "remedy": "Route ComfyUI's prompts through the broker."}]),
        panel("doubled", "Models loaded more than once",
              "The waste this exists to stop, named while it is happening.",
              [{"family": f, "hosts": ", ".join(hs), "copies": len(hs),
                "recall": "; ".join("%s: %s" % (n, copy_status(latest, (n, m), now))
                                    for n, m in list(hs.items())[1:])}
               for f, hs in sorted(doubled.items())],
              "nothing matched",
              "No model is loaded twice.",
              unreachable),
        panel("gpu", "Who holds the GPU" + (" of %s GiB" % cap if cap else ""),
              "Per-process GPU memory, from Windows performance counters.",
              held,
              "nothing matched" if readable else "denied",
              "No process holds more than 500 MB." if readable else
              "The GPU performance counters could not be read on this machine.",
              [] if readable else
              [{"what": "every process", "absence": "denied",
                "because": "Get-Counter for GPU Process Memory returned nothing usable.",
                "remedy": "Run the broker in an interactive session; session 0 cannot see them."}]),
    ]
    return {"version": 1, "at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
            "took_ms": int((time.time() - t0) * 1000), "source": "resident-broker",
            "findings": out}


def guarded(f):
    def go(self):
        self.replied = False
        try:
            f(self)
        except Exception as e:
            note("handler-error", path=self.path, error="%s: %s" % (type(e).__name__, e))
            if not self.replied:
                try:
                    self.send_json(500, {"error": {"message": "broker fault"}})
                except OSError:
                    pass
    return go


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    replied = False

    def log_message(self, *a):
        pass

    def send_json(self, code, obj):
        b = json.dumps(obj).encode()
        self.replied = True
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    @guarded
    def do_GET(self):
        if self.path == "/":
            b = PAGE.encode()
            self.replied = True
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(b)))
            self.end_headers()
            return self.wfile.write(b)
        if self.path.startswith("/residency"):
            with LOCK:
                return self.send_json(200, dict(STATE))
        if self.path.startswith("/findings"):
            return self.send_json(200, findings())
        if self.path.startswith("/v1/models"):
            fams = set()
            for s in cached().values():
                fams |= set(s.get("catalogue", {}))
            return self.send_json(200, {"object": "list", "data": [
                {"id": f, "object": "model", "owned_by": "resident"} for f in sorted(fams)]})
        self.send_json(404, {"error": "not found"})

    @guarded
    def do_POST(self):
        if not self.path.startswith("/v1/chat/completions"):
            return self.send_json(404, {"error": "not found"})
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        want, body["stream"] = family(body.get("model", "")), False
        skip = []
        for _ in range(len(HOSTS)):
            host, target = place(want, skip)
            if host is None:
                break
            body["model"] = target
            try:
                status, raw = http("POST", host.chat_url(), body)
                out = json.loads(raw)
            except urllib.error.HTTPError as e:
                return self.send_json(e.code, {"error": {"message": e.read().decode()[:400]}})
            except (OSError, ValueError) as e:
                host.sideline()
                skip.append(host.name)
                note("host-lost", family=want, host=host.name,
                     error="%s: %s" % (type(e).__name__, e))
                continue
            out["x_resident"] = {"host": host.name, "model": target, "family": want}
            return self.send_json(status, out)
        self.send_json(503, {"error": {"message": "no host holds %r" % want}})


PAGE = """<!doctype html><meta charset=utf-8><title>resident</title>
<style>
 body{font:14px ui-sans-serif,system-ui;margin:0;background:#101214;color:#e6e8ea;display:grid;
      grid-template-columns:1fr 1fr;height:100vh}
 section{padding:18px 22px;overflow:auto}
 #left{border-right:1px solid #2a2e33}
 h1{font-size:13px;letter-spacing:.14em;text-transform:uppercase;color:#8b939c;margin:0 0 14px}
 .host{border:1px solid #2a2e33;border-radius:8px;padding:10px 12px;margin-bottom:8px}
 .host b{font-size:13px}
 .down{color:#6b7076}
 .res{color:#7ee787;font-family:ui-monospace,monospace;font-size:12px}
 .dup{border:1px solid #6d3a12;background:#2a1a0c;border-radius:8px;padding:9px 12px;margin-bottom:8px;
      color:#ffa657;font-size:12px}
 .none{color:#6b7076;font-size:12px}
 .bar{position:relative;display:grid;grid-template-columns:130px 1fr 74px;align-items:center;gap:8px;
      font-size:12px;padding:3px 0}
 .bar i{display:block;height:9px;background:#2f81f7;border-radius:5px;min-width:2px}
 .bar b{font-family:ui-monospace,monospace;font-weight:400;color:#8b939c;text-align:right}
 .dec{font-family:ui-monospace,monospace;font-size:12px;padding:3px 0;border-bottom:1px solid #1c1f23}
 .reuse{color:#7ee787}.load{color:#ffa657}.host-lost,.load-failed,.poll-error{color:#ff7b72}
 #log{white-space:pre-wrap;line-height:1.5}
 textarea{width:100%;box-sizing:border-box;background:#17191c;color:#e6e8ea;border:1px solid #2a2e33;
          border-radius:8px;padding:10px;font:14px inherit;resize:vertical}
 button{margin-top:8px;background:#2f81f7;border:0;color:#fff;padding:8px 16px;border-radius:7px;
        font:600 13px inherit;cursor:pointer}
 .who{color:#8b939c;font-size:12px;margin-top:10px}
</style>
<section id=left><h1>What is resident</h1><div id=dup></div><div id=hosts></div>
<h1 style="margin-top:22px">Who holds the GPU <span id=held></span></h1><div id=hold></div>
<h1 style="margin-top:22px">Routing decisions</h1><div id=dec></div></section>
<section><h1>Chat &mdash; through the broker</h1>
<textarea id=q rows=3>Name three things a lighthouse keeper does at dawn.</textarea>
<button onclick=ask()>Send</button><div class=who id=who></div><div id=log></div></section>
<script>
async function tick(){
 const r = await (await fetch('/residency')).json();
 const holders = {};
 for(const [n,h] of Object.entries(r.hosts))
   for(const f of (h.resident_families||[])) (holders[f]=holders[f]||[]).push(n);
 const twice = Object.entries(holders).filter(([,hs])=>hs.length>1);
 document.getElementById('dup').innerHTML = twice.map(([f,hs])=>
  `<div class=dup><b>${f}</b> is loaded ${hs.length} times &mdash; ${hs.join(', ')}</div>`).join('');
 document.getElementById('hosts').innerHTML = Object.entries(r.hosts).map(([n,h])=>
  `<div class=host><b>${n}</b> ${h.up?'':`<span class=down>&mdash; ${h.reason||'not running'}</span>`}<br>`+
  (h.up?(h.resident&&h.resident.length?`<span class=res>${h.resident.join('<br>')}</span>`
       :'<span class=none>nothing loaded</span>'):'')+'</div>').join('');
 const cap = r.device_total_gib || 78.2;
 document.getElementById('held').textContent = r.gpu_readable
   ? `— ${(r.gpu_held_gib||0).toFixed(1)} of ${cap} GB` : '— counters not readable';
 document.getElementById('hold').innerHTML = (r.gpu_holders||[]).map(h=>
  `<div class=bar><span>${h.process}</span><i style="width:${Math.min(100,h.gib/cap*100)}%"></i>`+
  `<b>${h.gib.toFixed(1)} GB</b></div>`).join('') ||
  `<span class=none>${r.gpu_readable?'idle':'the GPU counters could not be read'}</span>`;
 document.getElementById('dec').innerHTML = (r.decisions||[]).map(d=>
  `<div class="dec ${d.kind}">${d.at} ${d.kind.toUpperCase()} ${d.host||''} &larr; ${d.family||d.error||''}</div>`).join('');
}
setInterval(tick,1500); tick();
async function ask(){
 const log=document.getElementById('log'); log.textContent='...';
 const t=performance.now();
 const r=await (await fetch('/v1/chat/completions',{method:'POST',
   headers:{'Content-Type':'application/json'},
   body:JSON.stringify({model:'qwen3.6-35b-a3b',max_tokens:200,
     messages:[{role:'user',content:document.getElementById('q').value}]})})).json();
 if(!r.choices){log.textContent=JSON.stringify(r);return;}
 const m=r.choices[0].message;
 log.textContent=m.content||m.reasoning_content||JSON.stringify(r);
 document.getElementById('who').textContent =
   `served by ${r.x_resident.host} / ${r.x_resident.model} in ${((performance.now()-t)/1000).toFixed(1)}s`;
}
</script>"""


class OnlyBroker(ThreadingHTTPServer):
    allow_reuse_address = False


if __name__ == "__main__":
    PORT = int(next((a for a in sys.argv[1:] if a.isdigit()),
                    os.environ.get("RESIDENT_PORT", "11800")))
    start_logging()
    try:
        server = OnlyBroker(("127.0.0.1", PORT), Handler)
    except OSError as e:
        log.info(json.dumps({"kind": "bind-failed", "port": PORT, "error": str(e)}))
        sys.exit(3)
    threading.Thread(target=poller, daemon=True).start()
    note("start", url="http://127.0.0.1:%d" % PORT, pid=os.getpid())
    server.serve_forever()
