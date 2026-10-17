"""Install only the owner-requested passive recorder on one existing host."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import subprocess
import zlib

OPS = Path(r"C:\Users\User\AppData\Local\Temp\maestro-s4-ops-559fb0072de143f097d5cff6044214fa")
HOSTS = {
    "S1": ("ssh-ops.conf", "current-s1"),
    "S2": ("ssh-s2-stage.conf", "mvpn-s2-jump"),
    "S3": ("ssh-s3-ops.conf", "proof-s3"),
    "S4": ("ssh-ops.conf", "proof-s4"),
}
REMOTE = r'''
import base64,hashlib,json,os,stat,subprocess,sys,time,tempfile
from pathlib import Path
phase="input"
created_unit=False
unit=""
def need(value):
    if not value: raise ValueError()
def run(args):
    return subprocess.run(args,capture_output=True,check=True,timeout=20).stdout.decode()
def write_owned(path,raw,mode):
    global created_unit
    if path.exists():
        info=path.lstat()
        need(stat.S_ISREG(info.st_mode) and info.st_uid==0 and not info.st_mode&0o022)
        before=path.read_bytes()
        if before==raw:return
        need(data.get("replace_source_sha"))
        if path.name=="observer.py":
            need(hashlib.sha256(before).hexdigest()==data["replace_source_sha"])
        else:
            expected=raw.replace(b"CapabilityBoundingSet=CAP_DAC_READ_SEARCH CAP_NET_RAW\n",
                                 b"CapabilityBoundingSet=CAP_DAC_READ_SEARCH CAP_NET_RAW CAP_SETUID CAP_SETGID\n")
            need(before.rstrip(b"\r\n")==expected.rstrip(b"\r\n"))
        backup=root/("before-"+hashlib.sha256(before).hexdigest()[:12]+"-"+path.name)
        if not backup.exists():
            fd=os.open(backup,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
            with os.fdopen(fd,"wb") as target:target.write(before)
        else:need(backup.read_bytes()==before)
        fd,name=tempfile.mkstemp(dir=path.parent,prefix=".cdn-watch-")
        with os.fdopen(fd,"wb") as target:target.write(raw);target.flush();os.fsync(target.fileno())
        os.chmod(name,mode);os.replace(name,path)
        return
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,mode)
    with os.fdopen(fd,"wb") as target:
        target.write(raw);target.flush();os.fsync(target.fileno())
    if path.name=="maestro-cdn-watch@.service":created_unit=True
try:
    need(os.geteuid()==0);os.umask(0o077)
    data=json.loads(sys.stdin.buffer.read(131072))
    node=data["node"];need(node in ("S1","S2","S3","S4"))
    source=base64.b64decode(data["source"],validate=True)
    service=base64.b64decode(data["service"],validate=True)
    need(hashlib.sha256(source).hexdigest()==data["source_sha256"])
    compile(source,"cdn-observer","exec")
    need(len(source)<65536 and len(service)<8192)
    root=Path("/opt/maestro-cdn-watch")
    disk=os.statvfs("/var/log")
    need(disk.f_bavail*disk.f_frsize>=64*1024*1024)
    phase="owned_files"
    root.mkdir(mode=0o755,exist_ok=True)
    info=root.lstat();need(stat.S_ISDIR(info.st_mode) and info.st_uid==0 and not info.st_mode&0o022)
    write_owned(root/"observer.py",source,0o755)
    write_owned(Path("/etc/systemd/system/maestro-cdn-watch@.service"),service,0o644)
    phase="start_recorder"
    unit="maestro-cdn-watch@"+node+".service"
    run(["systemctl","daemon-reload"])
    run(["systemctl","enable","--now",unit])
    if data.get("replace_source_sha"):
        run(["systemctl","restart",unit])
    phase="wait_first_log"
    log=Path("/var/log/maestro-cdn-watch/events.jsonl")
    deadline=time.monotonic()+20
    while True:
        state=dict(line.split("=",1) for line in run(["systemctl","show",unit,"-p","ActiveState","-p","SubState","-p","MainPID","-p","UnitFileState"]).splitlines())
        if state["ActiveState"]=="active" and state["SubState"]=="running" and int(state["MainPID"])>1 and log.exists() and log.stat().st_size>0:
            break
        need(time.monotonic()<deadline)
        time.sleep(.5)
    need(log.is_file() and stat.S_IMODE(log.stat().st_mode)==0o600)
    records=[json.loads(line) for line in log.read_text().splitlines()[-30:]]
    print(json.dumps({"installed":True,"node":node,"unit":unit,"state":state,"source_sha256":data["source_sha256"],
                      "log":str(log),"initial_kinds":sorted({r["kind"] for r in records}),
                      "initial_collector_errors":[{"collector":r.get("collector"),"error_type":r.get("error_type")} for r in records if r.get("kind")=="collector_error"],
                      "production_services_restarted":False,"maximum_log_bytes":32*1024*1024}))
except Exception as error:
    if created_unit and unit:
        subprocess.run(["systemctl","disable","--now",unit],capture_output=True,timeout=20)
    print(json.dumps({"installed":False,"phase":phase,"error_type":type(error).__name__,"details_redacted":True}))
    raise SystemExit(1)
'''

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("node", choices=HOSTS)
    parser.add_argument("--replace-source-sha")
    args = parser.parse_args()
    root = Path(__file__).resolve().parent
    source = (root / "cdn_watchdog.py").read_bytes()
    unit = (root / "maestro-cdn-watch@.service").read_bytes()
    payload = {"node": args.node, "source": base64.b64encode(source).decode(),
               "service": base64.b64encode(unit).decode(),
               "source_sha256": hashlib.sha256(source).hexdigest()}
    if args.replace_source_sha:
        if len(args.replace_source_sha) != 64 or any(c not in "0123456789abcdef" for c in args.replace_source_sha):
            raise ValueError("exact previous source hash required")
        payload["replace_source_sha"] = args.replace_source_sha
    config, host = HOSTS[args.node]
    encoded = base64.b64encode(zlib.compress(REMOTE.encode(), 9)).decode()
    remote = "python3 -B -c \"import base64,zlib;exec(zlib.decompress(base64.b64decode('" + encoded + "')))\""
    result = subprocess.run(
        ["ssh.exe", "-F", str(OPS / config), "-o", "BatchMode=yes",
         "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", host, remote],
        input=json.dumps(payload).encode(), capture_output=True, timeout=90)
    try:
        report = json.loads(result.stdout)
    except ValueError:
        report = {"installed": False, "node": args.node, "details_redacted": True}
    print(json.dumps(report, sort_keys=True))
    raise SystemExit(result.returncode if result.returncode else 0 if report.get("installed") else 1)

if __name__ == "__main__":
    main()
