package server

import (
	"net/http"
)

// uiHTML is the self-contained dashboard for Gate.
// Served at GET /ui — no build step, no external files.
const uiHTML = `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Gate — Stockyard</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Libre+Baskerville:ital,wght@0,400;0,700;1,400&family=JetBrains+Mono:wght@400;600&display=swap" rel="stylesheet">
<style>:root{
  --bg:#1a1410;--bg2:#241e18;--bg3:#2e261e;
  --rust:#c45d2c;--rust-light:#e8753a;--rust-dark:#8b3d1a;
  --leather:#a0845c;--leather-light:#c4a87a;
  --cream:#f0e6d3;--cream-dim:#bfb5a3;--cream-muted:#7a7060;
  --gold:#d4a843;--green:#5ba86e;--red:#c0392b;
  --font-serif:'Libre Baskerville',Georgia,serif;
  --font-mono:'JetBrains Mono',monospace;
}
*{margin:0;padding:0;box-sizing:border-box}
body{background:var(--bg);color:var(--cream);font-family:var(--font-serif);min-height:100vh;overflow-x:hidden}
a{color:var(--rust-light);text-decoration:none}a:hover{color:var(--gold)}
.hdr{background:var(--bg2);border-bottom:2px solid var(--rust-dark);padding:.9rem 1.8rem;display:flex;align-items:center;justify-content:space-between;gap:1rem}
.hdr-left{display:flex;align-items:center;gap:1rem}
.hdr-brand{font-family:var(--font-mono);font-size:.75rem;color:var(--leather);letter-spacing:3px;text-transform:uppercase}
.hdr-title{font-family:var(--font-mono);font-size:1.1rem;color:var(--cream);letter-spacing:1px}
.badge{font-family:var(--font-mono);font-size:.6rem;padding:.2rem .6rem;letter-spacing:1px;text-transform:uppercase;border:1px solid}
.badge-free{color:var(--green);border-color:var(--green)}
.badge-pro{color:var(--gold);border-color:var(--gold)}
.badge-ok{color:var(--green);border-color:var(--green)}
.badge-err{color:var(--red);border-color:var(--red)}
.main{max-width:1000px;margin:0 auto;padding:2rem 1.5rem}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:1rem;margin-bottom:2rem}
.card{background:var(--bg2);border:1px solid var(--bg3);padding:1.2rem 1.5rem}
.card-val{font-family:var(--font-mono);font-size:1.8rem;font-weight:700;color:var(--cream);display:block}
.card-lbl{font-family:var(--font-mono);font-size:.62rem;letter-spacing:2px;text-transform:uppercase;color:var(--leather);margin-top:.3rem}
.section{margin-bottom:2.5rem}
.section-title{font-family:var(--font-mono);font-size:.68rem;letter-spacing:3px;text-transform:uppercase;color:var(--rust-light);margin-bottom:.8rem;padding-bottom:.5rem;border-bottom:1px solid var(--bg3)}
table{width:100%;border-collapse:collapse;font-family:var(--font-mono);font-size:.78rem}
th{background:var(--bg3);padding:.5rem .8rem;text-align:left;color:var(--leather-light);font-weight:400;letter-spacing:1px;font-size:.65rem;text-transform:uppercase}
td{padding:.5rem .8rem;border-bottom:1px solid var(--bg3);color:var(--cream-dim);vertical-align:top;word-break:break-all}
tr:hover td{background:var(--bg2)}
.empty{color:var(--cream-muted);text-align:center;padding:2rem;font-style:italic}
.btn{font-family:var(--font-mono);font-size:.75rem;padding:.4rem 1rem;border:1px solid var(--leather);background:transparent;color:var(--cream);cursor:pointer;transition:all .2s}
.btn:hover{border-color:var(--rust-light);color:var(--rust-light)}
.btn-rust{border-color:var(--rust);color:var(--rust-light)}.btn-rust:hover{background:var(--rust);color:var(--cream)}
.pill{display:inline-block;font-family:var(--font-mono);font-size:.6rem;padding:.1rem .4rem;border-radius:2px;text-transform:uppercase}
.pill-get{background:#1a3a2a;color:var(--green)}.pill-post{background:#2a1f1a;color:var(--rust-light)}
.pill-del{background:#2a1a1a;color:var(--red)}.pill-ok{background:#1a3a2a;color:var(--green)}
.pill-err{background:#2a1a1a;color:var(--red)}
.mono{font-family:var(--font-mono);font-size:.78rem}
.lbl{font-family:var(--font-mono);font-size:.62rem;letter-spacing:1px;text-transform:uppercase;color:var(--leather)}
.upgrade{background:var(--bg2);border:1px solid var(--rust-dark);border-left:3px solid var(--rust);padding:.8rem 1.2rem;font-size:.82rem;color:var(--cream-dim);margin-bottom:1.5rem}
.upgrade a{color:var(--rust-light)}
pre{background:var(--bg3);padding:.8rem 1rem;font-family:var(--font-mono);font-size:.75rem;color:var(--cream-dim);overflow-x:auto;max-width:100%}
input,select{font-family:var(--font-mono);font-size:.78rem;background:var(--bg3);border:1px solid var(--bg3);color:var(--cream);padding:.4rem .7rem;outline:none}
input:focus,select:focus{border-color:var(--leather)}
.row{display:flex;gap:.8rem;align-items:flex-end;flex-wrap:wrap;margin-bottom:1rem}
.field{display:flex;flex-direction:column;gap:.3rem}
.sserow{padding:.4rem .8rem;border-bottom:1px solid var(--bg3);font-family:var(--font-mono);font-size:.72rem;color:var(--cream-dim);display:grid;grid-template-columns:120px 60px 1fr;gap:.5rem}
.sserow:nth-child(odd){background:var(--bg2)}
</style></head><body>
<div class="hdr">
  <div class="hdr-left">
    <svg viewBox="0 0 64 64" width="22" height="22" fill="none"><rect x="8" y="8" width="8" height="48" rx="2.5" fill="#e8753a"/><rect x="28" y="8" width="8" height="48" rx="2.5" fill="#e8753a"/><rect x="48" y="8" width="8" height="48" rx="2.5" fill="#e8753a"/><rect x="8" y="27" width="48" height="7" rx="2.5" fill="#c4a87a"/></svg>
    <span class="hdr-brand">Stockyard</span>
    <span class="hdr-title">Gate</span>
  </div>
  <div style="display:flex;gap:.8rem;align-items:center">
    <span id="tier-badge" class="badge badge-free">Free</span>
    <a href="/api/stats" class="lbl" style="color:var(--leather)">API</a>
    <a href="https://stockyard.dev/gate/" class="lbl" style="color:var(--leather)">Docs</a>
  </div>
</div>
<div class="main">

<div id="upgrade-banner" class="upgrade" style="display:none">
  <strong style="font-family:var(--font-mono);font-size:.7rem;color:var(--gold)">Free Tier</strong> &mdash;
  Unlimited upstreams, users, per-route rate limits, log export and more at
  <a href="https://stockyard.dev/gate/">stockyard.dev/gate</a>
</div>

<div class="cards">
  <div class="card"><span class="card-val" id="s-reqs">—</span><span class="card-lbl">Requests (24h)</span></div>
  <div class="card"><span class="card-val" id="s-err">—</span><span class="card-lbl">Errors (24h)</span></div>
  <div class="card"><span class="card-val" id="s-lat">—</span><span class="card-lbl">Avg Latency</span></div>
  <div class="card"><span class="card-val" id="s-keys">—</span><span class="card-lbl">Active Keys</span></div>
  <div class="card"><span class="card-val" id="s-users">—</span><span class="card-lbl">Users</span></div>
</div>

<div style="display:grid;grid-template-columns:1fr 1fr;gap:2rem">
<div class="section">
  <div class="section-title">API Keys</div>
  <div class="row">
    <div class="field"><span class="lbl">Name</span><input id="key-name" placeholder="alice" style="width:140px"></div>
    <div class="field"><span class="lbl">Role</span>
      <select id="key-role"><option>user</option><option>admin</option></select></div>
    <button class="btn btn-rust" onclick="createKey()">+ Issue Key</button>
  </div>
  <div id="new-key-banner" style="display:none;margin-bottom:.8rem;padding:.6rem .8rem;background:var(--bg3);border-left:3px solid var(--gold)">
    <span class="lbl" style="color:var(--gold)">New key (save it — shown once):</span><br>
    <span id="new-key-val" class="mono" style="color:var(--cream);word-break:break-all;font-size:.72rem"></span>
  </div>
  <table><thead><tr><th>Prefix</th><th>Name</th><th>Role</th><th>Last Used</th><th></th></tr></thead>
  <tbody id="key-list"><tr><td colspan="5" class="empty">Loading...</td></tr></tbody></table>
</div>
<div class="section">
  <div class="section-title">Users</div>
  <div class="row">
    <div class="field"><span class="lbl">Username</span><input id="user-name" placeholder="bob" style="width:130px"></div>
    <div class="field"><span class="lbl">Password</span><input id="user-pass" type="password" placeholder="••••••" style="width:120px"></div>
    <button class="btn btn-rust" onclick="createUser()">+ Add</button>
  </div>
  <table><thead><tr><th>Username</th><th>Role</th><th>Created</th><th></th></tr></thead>
  <tbody id="user-list"><tr><td colspan="4" class="empty">Loading...</td></tr></tbody></table>
</div>
</div>

<div class="section">
  <div class="section-title">Access Log <span class="lbl">(last 30)</span></div>
  <table><thead><tr><th>Method</th><th>Path</th><th>Status</th><th>Latency</th><th>Key</th><th>IP</th><th>Time</th></tr></thead>
  <tbody id="log-list"><tr><td colspan="7" class="empty">Loading...</td></tr></tbody></table>
</div>
</div>
<script>
let _timer=null;
function autoReload(fn,ms=8000){if(_timer)clearInterval(_timer);_timer=setInterval(fn,ms)}
function ts(s){if(!s)return'-';const d=new Date(s);return d.toLocaleString()}
function rel(s){if(!s)return'-';const d=new Date(s),n=new Date(),diff=Math.round((n-d)/1000);if(diff<60)return diff+'s ago';if(diff<3600)return Math.round(diff/60)+'m ago';return Math.round(diff/3600)+'h ago'}
function fmt(n){return n===undefined||n===null?'-':n.toLocaleString()}
function pill(m){const c={'GET':'pill-get','POST':'pill-post','DELETE':'pill-del'}[m]||'';return '<span class="pill '+c+'">'+m+'</span>'}
function status(s){const ok=s>=200&&s<300;return '<span class="pill '+(ok?'pill-ok':'pill-err')+'">'+s+'</span>'}

const API='/gate/api';

async function af(url,opts){
  const r=await fetch(url,opts).catch(()=>null);
  if(!r)return null;
  return {ok:r.ok,status:r.status,data:await r.json().catch(()=>({})) };
}

async function loadStats(){
  const r=await af(API+'/stats');
  if(!r||!r.ok)return;
  const s=r.data;
  document.getElementById('s-reqs').textContent=fmt(s.requests_24h);
  document.getElementById('s-err').textContent=fmt(s.errors_24h);
  document.getElementById('s-lat').textContent=(s.avg_latency_ms||0).toFixed(0)+'ms';
  document.getElementById('s-keys').textContent=fmt(s.active_keys);
  document.getElementById('s-users').textContent=fmt(s.users);
}

async function loadKeys(){
  const r=await af(API+'/keys');
  if(!r)return;
  const ks=r.data.keys||[];
  document.getElementById('key-list').innerHTML=ks.length?ks.map(k=>
    ` + "`" + `<tr>
      <td class="mono" style="color:var(--leather-light)">${k.key_prefix}</td>
      <td style="color:var(--cream)">${k.name||'—'}</td>
      <td><span class="pill pill-get" style="color:var(--leather-light)">${k.role}</span></td>
      <td>${k.last_used?rel(k.last_used):'never'}</td>
      <td><button class="btn" style="font-size:.65rem;padding:.2rem .5rem" onclick="deleteKey(${k.id})">Revoke</button></td>
    </tr>` + "`" + `).join(''):'<tr><td colspan="5" class="empty">No keys yet.</td></tr>';
}

async function loadUsers(){
  const r=await af(API+'/users');
  if(!r)return;
  const us=r.data.users||[];
  document.getElementById('user-list').innerHTML=us.length?us.map(u=>
    ` + "`" + `<tr>
      <td style="color:var(--cream)">${u.username}</td>
      <td class="mono">${u.role}</td>
      <td>${rel(u.created_at)}</td>
      <td><button class="btn" style="font-size:.65rem;padding:.2rem .5rem" onclick="deleteUser(${u.id})">Remove</button></td>
    </tr>` + "`" + `).join(''):'<tr><td colspan="4" class="empty">No users yet.</td></tr>';
}

async function loadLogs(){
  const r=await af(API+'/logs?limit=30');
  if(!r)return;
  const ls=r.data.logs||[];
  document.getElementById('log-list').innerHTML=ls.length?ls.map(l=>
    ` + "`" + `<tr>
      <td>${pill(l.method)}</td>
      <td class="mono" style="font-size:.7rem;max-width:200px;overflow:hidden;text-overflow:ellipsis">${l.path}</td>
      <td>${status(l.status)}</td>
      <td class="mono">${l.latency_ms}ms</td>
      <td class="mono" style="font-size:.7rem;color:var(--leather-light)">${l.key_prefix||'session'}</td>
      <td class="mono" style="font-size:.7rem">${l.source_ip||'—'}</td>
      <td>${rel(l.created_at)}</td>
    </tr>` + "`" + `).join(''):'<tr><td colspan="7" class="empty">No requests logged yet.</td></tr>';
}

async function createKey(){
  const name=document.getElementById('key-name').value.trim();
  const role=document.getElementById('key-role').value;
  const r=await af(API+'/keys',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name,role})});
  if(!r)return;
  if(r.status===402){alert('Free tier: 5 user limit reached. Upgrade to Pro at stockyard.dev/gate/');return;}
  if(r.data.key){
    document.getElementById('new-key-val').textContent=r.data.key;
    document.getElementById('new-key-banner').style.display='block';
    document.getElementById('key-name').value='';
    loadKeys();
  }
}

async function createUser(){
  const username=document.getElementById('user-name').value.trim();
  const password=document.getElementById('user-pass').value;
  if(!username||!password)return;
  const r=await af(API+'/users',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({username,password})});
  if(!r)return;
  if(r.status===402){alert('Free tier: 5 user limit reached. Upgrade to Pro at stockyard.dev/gate/');return;}
  document.getElementById('user-name').value='';
  document.getElementById('user-pass').value='';
  loadUsers();
}

async function deleteKey(id){if(!confirm('Revoke this key?'))return;await af(API+'/keys/'+id,{method:'DELETE'});loadKeys();}
async function deleteUser(id){if(!confirm('Remove user?'))return;await af(API+'/users/'+id,{method:'DELETE'});loadUsers();}

async function refresh(){await Promise.all([loadStats(),loadKeys(),loadUsers(),loadLogs()]);}
refresh();autoReload(refresh,8000);
</script></body></html>`

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(uiHTML))
}
