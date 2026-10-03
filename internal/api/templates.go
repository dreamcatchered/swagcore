package api

// HTML-шаблоны админки.
//
// v0.6.0 — переписан интерфейс. Что изменилось по сути:
//   - Дашборд отвечает на вопрос «что сломано и что делать сейчас», а не
//     просто показывает цифры (раньше реальные события тонули в reconcile-шуме).
//   - Страница «Добавить ноду» — одна большая кнопка и одна команда, без
//     «прочитай README, разберись с флагами».
//   - Везде пустые состояния объясняют следующий шаг.
//   - Понятные русские подписи вместо «desired_stopped», «placement: all».
//   - Тёмная тема выдержана, мобильная раскладка не разваливается.

const tplRoot = `{{define "head"}}<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark">
<title>{{.Title}} · swagCore</title>
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
<style>
:root{
  --bg:#0a0c11; --panel:#111520; --panel2:#171c2a; --panel3:#1e2434;
  --border:#252c3d; --border2:#333c52;
  --text:#e9edf6; --muted:#8b95ad; --dim:#5f6980;
  --accent:#5b8cff; --accent2:#8b6dff; --ok:#3ddc84; --warn:#ffb020; --err:#ff6b6b;
  --r:12px; --r2:9px;
}
*{box-sizing:border-box}
html,body{margin:0;padding:0}
body{background:var(--bg);color:var(--text);font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Inter,sans-serif;-webkit-font-smoothing:antialiased}
a{color:var(--accent);text-decoration:none}
a:hover{text-decoration:underline}
code,.mono,pre{font-family:ui-monospace,"SF Mono",Cascadia Code,Consolas,monospace}

/* ---------- каркас ---------- */
.layout{display:flex;min-height:100vh}
.sidebar{width:230px;flex:0 0 230px;background:var(--panel);border-right:1px solid var(--border);
  padding:20px 0;display:flex;flex-direction:column;position:sticky;top:0;height:100vh;overflow-y:auto}
.brand{display:flex;align-items:center;gap:9px;padding:0 20px 16px;font-weight:700;font-size:18px;letter-spacing:-.02em}
.brand .dotmark{width:9px;height:9px;border-radius:50%;background:linear-gradient(135deg,var(--accent),var(--accent2));
  box-shadow:0 0 10px var(--accent)}
.brand small{display:block;font-size:11px;color:var(--dim);font-weight:500;letter-spacing:0}
.navlink{display:flex;align-items:center;gap:10px;padding:9px 20px;color:var(--muted);font-size:13.5px;
  border-left:2px solid transparent;transition:background .12s,color .12s}
.navlink:hover{background:var(--panel2);color:var(--text);text-decoration:none}
.navlink.active{background:var(--panel2);color:var(--text);border-left-color:var(--accent);font-weight:500}
.navlink .ic{width:17px;text-align:center;opacity:.85}
.sidebar-foot{margin-top:auto;padding:14px 20px 0;border-top:1px solid var(--border);font-size:11.5px;color:var(--dim)}
.sidebar-foot a{display:block;padding:4px 0;color:var(--dim)}
.sidebar-foot a:hover{color:var(--muted)}
.navspacer{flex:1}
.content{flex:1;min-width:0;padding:26px 32px 70px;max-width:1420px}

/* ---------- типографика ---------- */
h1{font-size:24px;margin:0 0 4px;letter-spacing:-.02em;font-weight:650}
h2{font-size:12px;margin:30px 0 12px;color:var(--dim);text-transform:uppercase;letter-spacing:.09em;font-weight:650}
h3{font-size:15px;margin:0 0 10px;font-weight:600}
p{margin:8px 0}
.sub{color:var(--muted);font-size:13px;margin:0}
.hint{color:var(--dim);font-size:12.5px;margin:8px 0 0;line-height:1.55}
a.link-plain{color:var(--text)}
.mono-id{font-size:11.5px;color:var(--dim)}

/* ---------- шапка страницы ---------- */
.pagehead{display:flex;justify-content:space-between;align-items:flex-start;gap:16px;flex-wrap:wrap;margin-bottom:20px}
.pagehead .actions{display:flex;gap:8px;flex-wrap:wrap;align-items:center}
.pagehead form{margin:0}

/* ---------- кнопки ---------- */
.btn{display:inline-flex;align-items:center;justify-content:center;gap:7px;background:var(--accent);color:#fff;
  border:1px solid transparent;border-radius:var(--r2);padding:8px 15px;font-size:13.5px;font-weight:550;
  cursor:pointer;font-family:inherit;transition:filter .12s,transform .06s;white-space:nowrap}
.btn:hover{filter:brightness(1.14);text-decoration:none;color:#fff}
.btn:active{transform:translateY(1px)}
.btn[disabled]{opacity:.5;cursor:not-allowed}
.btn.ghost{background:var(--panel3);color:var(--text);border-color:var(--border2)}
.btn.ghost:hover{background:#252c3e}
.btn.ok{background:var(--ok);color:#06180d}
.btn.danger{background:transparent;color:var(--err);border-color:rgba(255,107,107,.4)}
.btn.danger:hover{background:rgba(255,107,107,.14);color:var(--err)}
.btn.sm{padding:5px 11px;font-size:12.5px;border-radius:7px}
.btn.lg{padding:12px 22px;font-size:15px;border-radius:var(--r)}
.btnrow{display:flex;gap:6px;flex-wrap:wrap}
form.inline{display:inline}

/* ---------- карточки/статистика ---------- */
.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:12px;margin-bottom:6px}
.stat{background:linear-gradient(165deg,var(--panel),var(--panel2));border:1px solid var(--border);
  border-radius:var(--r);padding:15px 17px}
.stat .v{font-size:26px;font-weight:680;letter-spacing:-.03em;line-height:1.15}
.stat .k{color:var(--muted);font-size:12px;margin-top:3px}
.stat .x{color:var(--dim);font-size:11.5px;margin-top:5px}
.stat.good .v{color:var(--ok)} .stat.bad .v{color:var(--err)} .stat.warn .v{color:var(--warn)}

/* ---------- панели ---------- */
.panel{background:var(--panel);border:1px solid var(--border);border-radius:var(--r);padding:18px 20px;margin:14px 0}
.panel.accent{border-color:rgba(91,140,255,.45);background:linear-gradient(165deg,rgba(91,140,255,.07),var(--panel))}
.panel.good{border-color:rgba(61,220,132,.4);background:linear-gradient(165deg,rgba(61,220,132,.06),var(--panel))}
.panel.danger{border-color:rgba(255,107,107,.4);background:linear-gradient(165deg,rgba(255,107,107,.06),var(--panel))}
.grid2{display:grid;grid-template-columns:1fr 1fr;gap:14px}
.grid3{display:grid;grid-template-columns:repeat(auto-fit,minmax(300px,1fr));gap:14px}

/* ---------- проблемы ---------- */
.issues{display:flex;flex-direction:column;gap:1px;background:var(--border);border:1px solid var(--border);
  border-radius:var(--r);overflow:hidden;margin:12px 0}
.issue{display:flex;gap:11px;align-items:flex-start;padding:11px 15px;background:var(--panel);font-size:13.5px}
.issue .tag{flex:0 0 auto;font-size:11px;font-weight:650;padding:2px 9px;border-radius:20px;margin-top:1px}
.issue.err .tag{background:rgba(255,107,107,.16);color:var(--err)}
.issue.warn .tag{background:rgba(255,176,32,.16);color:var(--warn)}
.issue.info .tag{background:rgba(91,140,255,.16);color:var(--accent)}
.issue.ok .tag{background:rgba(61,220,132,.16);color:var(--ok)}

/* ---------- таблицы ---------- */
.tablewrap{border:1px solid var(--border);border-radius:var(--r);overflow:hidden;overflow-x:auto;background:var(--panel)}
table{width:100%;border-collapse:collapse;font-size:13px}
th,td{padding:10px 14px;text-align:left;border-bottom:1px solid var(--border);vertical-align:top}
th{background:#0d1017;color:var(--dim);font-size:11px;text-transform:uppercase;letter-spacing:.06em;font-weight:650;white-space:nowrap}
tbody tr:last-child td{border-bottom:none}
tbody tr:hover{background:var(--panel2)}
td.num{text-align:right;font-variant-numeric:tabular-nums;white-space:nowrap}
.empty{padding:26px 20px;text-align:center;color:var(--dim);font-size:13.5px;background:var(--panel)}
.empty a{font-weight:550}

/* ---------- бейджи статусов ---------- */
.badge{display:inline-flex;align-items:center;gap:5px;padding:2px 9px;border-radius:20px;font-size:11.5px;font-weight:600;white-space:nowrap}
.badge::before{content:"";width:6px;height:6px;border-radius:50%;background:currentColor;flex:0 0 auto}
.badge.nodot::before{display:none}
.b-online,.b-running,.b-ok{background:rgba(61,220,132,.14);color:var(--ok)}
.b-offline,.b-stopped,.b-disabled{background:rgba(139,149,173,.14);color:var(--muted)}
.b-starting,.b-desired_running,.b-warn,.b-orphan{background:rgba(255,176,32,.15);color:var(--warn)}
.b-failed,.b-error,.b-desired_stopped{background:rgba(255,107,107,.15);color:var(--err)}

/* ---------- метрики ноды ---------- */
.nodecards{display:grid;grid-template-columns:repeat(auto-fill,minmax(360px,1fr));gap:14px}
.nodecard{background:linear-gradient(168deg,var(--panel) 0%,var(--panel2) 100%);border:1px solid var(--border);
  border-radius:14px;padding:17px 19px;transition:border-color .15s}
.nodecard:hover{border-color:var(--border2)}
.nodecard.off{border-color:rgba(255,107,107,.32)}
.nodecard .nhead{display:flex;justify-content:space-between;align-items:center;gap:10px;margin-bottom:6px}
.nodecard .nname{font-size:16px;font-weight:620;display:flex;align-items:center;gap:8px;min-width:0}
.nodecard .nname a{color:var(--text)}
.nodecard .nname a:hover{color:var(--accent);text-decoration:none}
.nmeta{color:var(--muted);font-size:12px;margin-bottom:13px;display:flex;gap:7px;flex-wrap:wrap;align-items:center}
.nmeta .sep{color:var(--dim)}
.meters{display:grid;grid-template-columns:repeat(3,1fr);gap:11px;margin-bottom:12px}
.meter .ml{font-size:10.5px;color:var(--dim);text-transform:uppercase;letter-spacing:.06em}
.meter .mv{font-size:13.5px;font-weight:600;margin:2px 0 5px;font-variant-numeric:tabular-nums}
.bar{height:6px;background:#0a0c11;border-radius:4px;overflow:hidden;border:1px solid var(--border)}
.bar i{display:block;height:100%;border-radius:3px;background:linear-gradient(90deg,var(--accent),var(--accent2))}
.bar i.ok{background:var(--ok)} .bar i.warn{background:var(--warn)} .bar i.err{background:var(--err)}
.nfoot{display:flex;justify-content:space-between;align-items:center;gap:10px;flex-wrap:wrap;
  border-top:1px solid var(--border);padding-top:12px;margin-top:4px;font-size:12.5px;color:var(--muted)}

/* ---------- карточки проектов ---------- */
.projcards{display:grid;grid-template-columns:repeat(auto-fill,minmax(390px,1fr));gap:14px}
.projcard{background:var(--panel);border:1px solid var(--border);border-radius:14px;padding:17px 19px}
.projcard:hover{border-color:var(--border2)}
.projcard .phead{display:flex;justify-content:space-between;align-items:center;gap:10px}
.projcard .pname{font-size:16px;font-weight:620}
.projcard .pname a{color:var(--text)}
.projcard .pname a:hover{color:var(--accent);text-decoration:none}
.pline{color:var(--muted);font-size:12.5px;margin:7px 0 0}
.chips{display:flex;gap:6px;flex-wrap:wrap;margin:11px 0 0}
.chip{background:var(--panel3);border:1px solid var(--border);border-radius:7px;padding:3px 9px;font-size:11.5px;color:var(--muted)}
.chip.err{border-color:rgba(255,107,107,.4);color:var(--err);background:rgba(255,107,107,.08)}
.chip.warn{border-color:rgba(255,176,32,.4);color:var(--warn);background:rgba(255,176,32,.08)}
.chip.ok{border-color:rgba(61,220,132,.35);color:var(--ok);background:rgba(61,220,132,.07)}

/* ---------- формы ---------- */
input[type=text],input[type=password],input[type=number],select,textarea{
  background:#080a0e;color:var(--text);border:1px solid var(--border2);border-radius:var(--r2);
  padding:9px 12px;font-size:14px;font-family:inherit;width:100%}
input:focus,select:focus,textarea:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 3px rgba(91,140,255,.14)}
select{cursor:pointer}
textarea{font-family:ui-monospace,Consolas,monospace;font-size:12.5px;line-height:1.6;min-height:230px;resize:vertical}
label.fl{display:flex;flex-direction:column;gap:5px;font-size:12px;color:var(--muted);font-weight:500}
label.fl input,label.fl select{min-width:0}
.formgrid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:11px;align-items:end}
fieldset{border:1px solid var(--border);border-radius:var(--r2);padding:14px 16px;margin:0 0 14px}
legend{padding:0 7px;font-size:12px;color:var(--dim);text-transform:uppercase;letter-spacing:.06em}

/* ---------- блок кода с копированием ---------- */
.cmdblock{background:#07090d;border:1px solid var(--border2);border-radius:var(--r2);
  padding:13px 15px;display:flex;align-items:center;gap:11px}
.cmdblock code{flex:1;font-size:12.5px;line-height:1.6;color:#a8d0ff;word-break:break-all;user-select:all}
.cmdblock .btn{flex:0 0 auto}
.stepnum{display:inline-flex;align-items:center;justify-content:center;width:22px;height:22px;border-radius:50%;
  background:var(--accent);color:#fff;font-size:12px;font-weight:700;flex:0 0 auto}

/* ---------- скриншоты ---------- */
.shots{display:grid;grid-template-columns:repeat(auto-fill,minmax(230px,1fr));gap:12px}
.shots a{display:block;border:1px solid var(--border);border-radius:var(--r2);overflow:hidden;background:#07090d}
.shots img{display:block;width:100%;height:auto}
.shots .cap{padding:6px 9px;font-size:11px;color:var(--dim);border-top:1px solid var(--border);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}

/* ---------- логи ---------- */
pre.log{background:#07090d;border:1px solid var(--border);border-radius:var(--r2);padding:14px;
  overflow:auto;max-height:520px;font-size:12px;line-height:1.55;white-space:pre-wrap;word-break:break-word}
pre.out{background:#07090d;border:1px solid var(--border);border-radius:6px;padding:9px 11px;
  margin:0;font-size:11.5px;max-height:220px;overflow:auto;white-space:pre-wrap;word-break:break-word}

/* ---------- тост ---------- */
.toast{position:fixed;bottom:22px;right:22px;background:var(--panel3);border:1px solid var(--accent);
  color:var(--text);border-radius:var(--r2);padding:12px 18px;font-size:13.5px;font-weight:500;
  opacity:0;transform:translateY(10px);transition:opacity .22s,transform .22s;pointer-events:none;z-index:100;max-width:420px}
.toast.show{opacity:1;transform:none}
.toast.err{border-color:var(--err)}

/* ---------- палитра команд ---------- */
.palette{display:flex;gap:6px;flex-wrap:wrap;margin-top:10px}
.palette .btn{font-size:11.5px;padding:4px 10px;background:var(--panel3);color:var(--muted);border-color:var(--border)}
.palette .btn:hover{color:var(--text);background:var(--panel3)}

/* ---------- мобильная ---------- */
@media(max-width:1000px){ .grid2{grid-template-columns:1fr} }
@media(max-width:820px){
  .layout{flex-direction:column}
  .sidebar{width:100%;flex:none;height:auto;position:static;flex-direction:row;overflow-x:auto;padding:10px 12px;align-items:center;gap:4px}
  .brand{padding:0 12px 0 4px;border:none;font-size:15px}
  .brand small{display:none}
  .navlink{border-left:none;border-radius:7px;padding:7px 12px;white-space:nowrap}
  .navlink.active{border-left:none;background:var(--accent);color:#fff}
  .sidebar-foot{display:none}
  .content{padding:18px 16px 60px}
  h1{font-size:20px}
  .meters{grid-template-columns:1fr 1fr}
}
</style>
</head>
<body><div class="layout">
<nav class="sidebar">
  <div class="brand"><span class="dotmark"></span><div>swagCore<small>панель управления платформой</small></div></div>
  <a href="/"       class="navlink {{if eq .Active "dashboard"}}active{{end}}"><span class="ic">◈</span>Обзор</a>
  <a href="/nodes"  class="navlink {{if eq .Active "nodes"}}active{{end}}"><span class="ic">▤</span>Ноды</a>
  <a href="/projects" class="navlink {{if eq .Active "projects"}}active{{end}}"><span class="ic">◧</span>Проекты</a>
  <a href="/add-node" class="navlink {{if eq .Active "addnode"}}active{{end}}"><span class="ic">＋</span>Добавить ноду</a>
  <a href="/events" class="navlink {{if eq .Active "events"}}active{{end}}"><span class="ic">≡</span>События</a>
  <a href="/ai"     class="navlink {{if eq .Active "ai"}}active{{end}}"><span class="ic">✦</span>ИИ-агент</a>
  <div class="navspacer"></div>
  <div class="sidebar-foot">
    <div style="margin-bottom:8px">сборка <span class="mono">{{.Version}}</span></div>
    <a href="/logout">→ Выйти</a>
  </div>
</nav>
<main class="content">{{end}}`

// ---------- Обзор ----------
const tplDashboard = `{{define "dashboard"}}{{template "head" .}}
<div class="pagehead">
  <div>
    <h1>Обзор платформы</h1>
    <p class="sub">Ноды, проекты и всё, что требует вашего внимания прямо сейчас.</p>
  </div>
  <div class="actions">
    <a href="/add-node"><button class="btn">＋ Добавить ноду</button></a>
    <a href="/projects"><button class="btn ghost">◧ Проекты</button></a>
  </div>
</div>

<div class="stats">
  <div class="stat {{if .Online}}{{else if .TotalNodes}}bad{{end}}">
    <div class="v">{{.Online}}<span style="font-size:16px;color:var(--dim)">/{{.TotalNodes}}</span></div>
    <div class="k">нод на связи</div>
    <div class="x">{{.DockerNodes}} из них с Docker</div>
  </div>
  <div class="stat">
    <div class="v">{{.Running}}<span style="font-size:16px;color:var(--dim)">/{{.TotalProjects}}</span></div>
    <div class="k">проектов работает</div>
    <div class="x">{{.Failed}} с ошибками</div>
  </div>
  <div class="stat">
    <div class="v">{{mb .MemUsed}}</div>
    <div class="k">занято памяти</div>
    <div class="x">из {{mb .MemTotal}}</div>
  </div>
  <div class="stat">
    <div class="v">{{gb .DiskUsed}}</div>
    <div class="k">занято диска</div>
    <div class="x">из {{gb .DiskTotal}}</div>
  </div>
  <div class="stat">
    <div class="v">{{printf "%.0f%%" .AvgCPU}}</div>
    <div class="k">средняя загрузка CPU</div>
    <div class="x">по всем нодам</div>
  </div>
</div>

{{if .Issues}}
<h2>Требует внимания</h2>
<div class="issues">
{{range .Issues}}
  <div class="issue {{.Level}}"><span class="tag">{{if eq .Level "err"}}проблема{{else if eq .Level "warn"}}внимание{{else if eq .Level "ok"}}ок{{else}}инфо{{end}}</span><span>{{.Text}}{{if .Link}} — <a href="{{.Link}}">открыть</a>{{end}}</span></div>
{{end}}
</div>
{{else}}
<div class="panel good" style="margin-top:16px">
  <h3>Всё в порядке</h3>
  <p class="sub">Все ноды на связи, проекты работают, ошибок в последних событиях нет.</p>
</div>
{{end}}

<h2>Ноды</h2>
{{if .Nodes}}
<div class="tablewrap">
<table>
<thead><tr>
  <th style="min-width:190px">Имя</th>
  <th style="width:150px">Состояние</th>
  <th style="width:110px">На связи</th>
  <th style="width:150px">Проекты</th>
  <th class="num" style="width:62px">CPU</th>
  <th class="num" style="width:150px">Память</th>
  <th class="num" style="width:150px">Диск</th>
</tr></thead>
<tbody>
{{$np := .NodeProj}}{{$npr := .NodeProjRun}}
{{range .Nodes}}
<tr>
  <td><a href="/nodes/{{.ID}}" class="link-plain"><b>{{.Hostname}}</b></a><br><span class="mono-id">{{.OS}}/{{.Arch}} · агент {{.AgentVer}}</span></td>
  <td>{{if eq .Status "online"}}<span class="badge b-online">на связи</span>{{else}}<span class="badge b-offline">не в сети</span>{{end}}{{if not .Enabled}}<br><span class="badge b-disabled nodot" style="margin-top:4px">выкл. планировщик</span>{{end}}</td>
  <td class="sub">{{since .LastSeen}}</td>
  <td><a href="/nodes/{{.ID}}">{{index $np .ID}} шт · {{index $npr .ID}} раб.</a></td>
  <td class="num">{{if .Metrics}}{{printf "%.0f%%" .Metrics.CPUPct}}{{else}}—{{end}}</td>
  <td class="num">{{if .Metrics}}{{mb .Metrics.MemUsedMB}} / {{mb .Metrics.MemTotalMB}}{{else}}—{{end}}</td>
  <td class="num">{{if .Metrics}}{{gb .Metrics.DiskUsedGB}} / {{gb .Metrics.DiskTotalGB}}{{else}}—{{end}}</td>
</tr>
{{end}}
</tbody></table>
</div>
{{else}}
<div class="empty">Нод пока нет.<br><br>
  <a href="/add-node"><button class="btn lg">Подключить первую ноду одной командой</button></a><br><br>
  <span style="color:var(--dim)">Это займёт меньше минуты: скрипт сам скачает агента, поставит его как службу и подключит машину.</span>
</div>
{{end}}

<h2>Проекты</h2>
{{if .Projects}}
<div class="tablewrap">
<table>
<thead><tr><th>Имя</th><th>Состояние</th><th>Режим</th><th>Публичный адрес</th><th>Обновлён</th></tr></thead>
<tbody>
{{range .Projects}}
<tr>
  <td><a href="/projects/{{.ID}}" class="link-plain"><b>{{.Name}}</b></a></td>
  <td><span class="badge b-{{.Status}}">{{if eq .Status "running"}}работает{{else if eq .Status "failed"}}ошибка{{else if eq .Status "starting"}}запускается{{else if eq .Status "stopped"}}остановлен{{else}}желание: {{.Status}}{{end}}</span></td>
  <td class="sub">{{if eq .Manifest.Mode "process"}}нативный процесс{{else}}Docker{{end}}{{if .Manifest.Image}} · {{.Manifest.Image}}{{end}}</td>
  <td>{{if .Manifest.Domain}}<a href="https://{{.Manifest.Domain}}" target="_blank" rel="noopener">{{.Manifest.Domain}}</a>{{else}}<span class="mono-id">не задан</span>{{end}}</td>
  <td class="sub">{{since .UpdatedAt}}</td>
</tr>
{{end}}
</tbody></table>
</div>
{{else}}
<div class="empty">Проектов нет.<br><br>
  <a href="/projects#deploy"><button class="btn">Задеплоить первый проект</button></a>
</div>
{{end}}

<h2>Последние события</h2>
{{if .Events}}
<div class="tablewrap">
<table>
<thead><tr><th style="width:150px">Время</th><th style="width:130px">Источник</th><th>Что произошло</th></tr></thead>
<tbody>
{{range .Events}}
<tr><td class="sub mono-id">{{.Time.Format "02.01 15:04:05"}}</td><td class="sub mono-id">{{.Source}}</td><td>{{.Message}}</td></tr>
{{end}}
</tbody></table>
</div>
{{else}}
<div class="empty">Событий пока нет.</div>
{{end}}

<p class="hint">База данных: {{mb .DBSize}} · WAL {{mb .WALSize}} · панель: <a href="{{.BaseURL}}" target="_blank" rel="noopener">{{.BaseURL}}</a></p>
{{template "foot" .}}{{end}}`

// ---------- Ноды (список) ----------
const tplNodes = `{{define "nodes"}}{{template "head" .}}
<div class="pagehead">
  <div><h1>Ноды</h1><p class="sub">Машины, которые выполняют ваши проекты. Нажмите на ноду, чтобы управлять ею.</p></div>
  <div class="actions">
    <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)">
      <input type="hidden" name="__do" value="/nodes/update-all">
      <button class="btn ghost" title="Отправить команду обновления всем нодам на связи">⟳ Обновить всех агентов</button>
    </form>
    <a href="/add-node"><button class="btn">＋ Добавить ноду</button></a>
  </div>
</div>

{{if .Nodes}}
<div class="nodecards">
{{$np := .NodeProj}}{{$npr := .NodeProjRun}}{{$usage := .Usage}}
{{range .Nodes}}
<div class="nodecard {{if ne .Status "online"}}off{{end}}">
  <div class="nhead">
    <div class="nname">
      {{if eq .Status "online"}}<span class="dot" style="width:8px;height:8px;border-radius:50%;background:var(--ok);display:inline-block;box-shadow:0 0 8px var(--ok)"></span>{{else}}<span class="dot" style="width:8px;height:8px;border-radius:50%;background:var(--dim);display:inline-block"></span>{{end}}
      <a href="/nodes/{{.ID}}">{{.Hostname}}</a>
    </div>
    <div style="display:flex;gap:6px;align-items:center">
      {{if not .Enabled}}<span class="badge b-disabled nodot">выкл.</span>{{end}}
      <span class="badge b-{{.Status}}">{{if eq .Status "online"}}на связи{{else}}не в сети{{end}}</span>
    </div>
  </div>
  <div class="nmeta">
    <span>{{.OS}}/{{.Arch}}</span><span class="sep">·</span>
    <span>агент {{.AgentVer}}</span><span class="sep">·</span>
    <span>{{if .HasDocker}}есть Docker{{else}}без Docker{{end}}</span>
    {{if .MaxMemMB}}<span class="sep">·</span><span>лимит {{mb .MaxMemMB}}</span>{{end}}
    {{if .MaxCPUs}}<span class="sep">·</span><span>{{printf "%.1f" .MaxCPUs}} CPU</span>{{end}}
    {{if .MaxDiskGB}}<span class="sep">·</span><span>{{.MaxDiskGB}} ГБ</span>{{end}}
  </div>

  <div class="meters">
    <div class="meter">
      <div class="ml">CPU</div><div class="mv">{{if .Metrics}}{{printf "%.0f%%" .Metrics.CPUPct}}{{else}}—{{end}}</div>
      {{if .Metrics}}<div class="bar"><i class="{{if ge .Metrics.CPUPct 90.0}}err{{else if ge .Metrics.CPUPct 60.0}}warn{{else}}ok{{end}}" style="width:{{pct .Metrics.CPUPct 100}}%"></i></div>{{end}}
    </div>
    <div class="meter">
      <div class="ml">Память</div><div class="mv">{{if .Metrics}}{{mb .Metrics.MemUsedMB}} / {{mb .Metrics.MemTotalMB}}{{else}}—{{end}}</div>
      {{if and .Metrics .Metrics.MemTotalMB}}<div class="bar"><i class="{{if ge (divf .Metrics.MemUsedMB .Metrics.MemTotalMB) 0.9}}err{{else if ge (divf .Metrics.MemUsedMB .Metrics.MemTotalMB) 0.6}}warn{{else}}ok{{end}}" style="width:{{pct .Metrics.MemUsedMB .Metrics.MemTotalMB}}%"></i></div>{{end}}
    </div>
    <div class="meter">
      <div class="ml">Диск</div><div class="mv">{{if .Metrics}}{{gb .Metrics.DiskUsedGB}} / {{gb .Metrics.DiskTotalGB}}{{else}}—{{end}}</div>
      {{if and .Metrics .Metrics.DiskTotalGB}}<div class="bar"><i class="{{if ge (divf .Metrics.DiskUsedGB .Metrics.DiskTotalGB) 0.9}}err{{else if ge (divf .Metrics.DiskUsedGB .Metrics.DiskTotalGB) 0.7}}warn{{else}}ok{{end}}" style="width:{{pct .Metrics.DiskUsedGB .Metrics.DiskTotalGB}}%"></i></div>{{end}}
    </div>
  </div>

  <div class="nfoot">
    <div>
      <b style="color:var(--text)">{{index $np .ID}}</b> проектов · <b style="color:var(--text)">{{index $npr .ID}}</b> работает
      {{if .MaxMemMB}}<br><span class="mono-id">занято платформой: {{mb (index (index $usage .ID) 0)}} из {{mb .MaxMemMB}}</span>{{end}}
      <br><span class="mono-id">на связи: {{since .LastSeen}}</span>
    </div>
    <div class="btnrow">
      <a href="/nodes/{{.ID}}"><button class="btn sm ghost">⚙ Управление</button></a>
      <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)">
        <input type="hidden" name="__do" value="/nodes/recon"><input type="hidden" name="id" value="{{.ID}}">
        <button class="btn sm ghost" title="Спросить у агента, что на самом деле запущено">опрос</button>
      </form>
      <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)">
        <input type="hidden" name="__do" value="/nodes/update"><input type="hidden" name="id" value="{{.ID}}">
        <button class="btn sm ghost" title="Скачать свежую сборку агента">⟳ агент</button>
      </form>
      <form class="inline" method="post" action="/api/action" onsubmit="return delNode(this,event,'{{.Hostname}}','{{.Status}}')">
        <input type="hidden" name="__do" value="/nodes/delete"><input type="hidden" name="id" value="{{.ID}}"><input type="hidden" name="force" value="1">
        <button class="btn sm danger">✕</button>
      </form>
    </div>
  </div>
</div>
{{end}}
</div>
{{else}}
<div class="panel">
  <h3>Подключите первую ноду</h3>
  <p class="sub">Нода — это любая машина (Windows или Linux), которая будет выполнять ваши проекты. Подключение занимает одну команду: скрипт сам всё скачает, поставит и настроит.</p>
  <p style="margin-top:16px"><a href="/add-node"><button class="btn lg">＋ Подключить ноду одной командой</button></a></p>
</div>
{{end}}
{{template "foot" .}}{{end}}`

// ---------- Нода (детальная) ----------
const tplNode = `{{define "node"}}{{template "head" .}}
<div class="pagehead">
  <div>
    <h1>{{.N.Hostname}}</h1>
    <div style="display:flex;gap:7px;align-items:center;flex-wrap:wrap;margin-top:6px">
      <span class="badge b-{{.N.Status}}">{{if eq .N.Status "online"}}на связи{{else}}не в сети{{end}}</span>
      {{if not .N.Enabled}}<span class="badge b-disabled nodot">выключена в планировщике</span>{{end}}
      <span class="mono-id">ID {{.N.ID}} · {{.N.OS}}/{{.N.Arch}} · агент {{.N.AgentVer}}</span>
    </div>
  </div>
  <div class="actions">
    <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="4000">
      <input type="hidden" name="__do" value="/nodes/screenshot"><input type="hidden" name="id" value="{{.N.ID}}">
      <button class="btn ghost">📷 Снимок экрана</button></form>
    <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="2000">
      <input type="hidden" name="__do" value="/nodes/update"><input type="hidden" name="id" value="{{.N.ID}}">
      <button class="btn ghost">⟳ Обновить агента</button></form>
    <form class="inline" method="post" action="/api/action" onsubmit="return confirmAct(this,event,'Перезагрузить {{.N.Hostname}}? Проекты на ней перезапустятся автоматически.')">
      <input type="hidden" name="__do" value="/nodes/power"><input type="hidden" name="id" value="{{.N.ID}}"><input type="hidden" name="action" value="reboot">
      <button class="btn ghost">↻ Перезагрузить</button></form>
    <form class="inline" method="post" action="/api/action" onsubmit="return confirmAct(this,event,'Выключить {{.N.Hostname}}?')">
      <input type="hidden" name="__do" value="/nodes/power"><input type="hidden" name="id" value="{{.N.ID}}"><input type="hidden" name="action" value="shutdown">
      <button class="btn danger">⏻ Выключить</button></form>
  </div>
</div>

<div class="stats">
  <div class="stat"><div class="v">{{if .N.Metrics}}{{printf "%.0f%%" .N.Metrics.CPUPct}}{{else}}—{{end}}</div>
    <div class="k">загрузка CPU</div>{{if .N.MaxCPUs}}<div class="x">лимит {{printf "%.1f" .N.MaxCPUs}} ядер</div>{{end}}
    {{if .N.Metrics}}<div class="bar" style="margin-top:7px"><i class="{{if ge .N.Metrics.CPUPct 90.0}}err{{else if ge .N.Metrics.CPUPct 60.0}}warn{{else}}ok{{end}}" style="width:{{pct .N.Metrics.CPUPct 100}}%"></i></div>{{end}}</div>
  <div class="stat"><div class="v">{{if .N.Metrics}}{{mb .N.Metrics.MemUsedMB}}{{else}}—{{end}}</div>
    <div class="k">занято памяти{{if .N.Metrics}} из {{mb .N.Metrics.MemTotalMB}}{{end}}</div>{{if .N.MaxMemMB}}<div class="x">лимит платформы {{mb .N.MaxMemMB}}</div>{{end}}
    {{if and .N.Metrics .N.Metrics.MemTotalMB}}<div class="bar" style="margin-top:7px"><i class="{{if ge (divf .N.Metrics.MemUsedMB .N.Metrics.MemTotalMB) 0.9}}err{{else if ge (divf .N.Metrics.MemUsedMB .N.Metrics.MemTotalMB) 0.6}}warn{{else}}ok{{end}}" style="width:{{pct .N.Metrics.MemUsedMB .N.Metrics.MemTotalMB}}%"></i></div>{{end}}</div>
  <div class="stat"><div class="v">{{if .N.Metrics}}{{gb .N.Metrics.DiskUsedGB}}{{else}}—{{end}}</div>
    <div class="k">занято диска{{if .N.Metrics}} из {{gb .N.Metrics.DiskTotalGB}}{{end}}</div>{{if .N.MaxDiskGB}}<div class="x">лимит платформы {{.N.MaxDiskGB}} ГБ</div>{{end}}</div>
  <div class="stat"><div class="v">{{if .N.Metrics}}{{printf "%.1f" (divf .N.Metrics.UptimeSec 3600)}}{{else}}—{{end}}<span style="font-size:15px;color:var(--dim)"> ч</span></div>
    <div class="k">работает без перезагрузки</div><div class="x">на связи {{since .N.LastSeen}}</div></div>
  <div class="stat"><div class="v">{{if .N.Metrics}}{{.N.Metrics.Containers}}{{else}}0{{end}}</div>
    <div class="k">контейнеров Docker</div><div class="x">IP: {{if .N.IP}}{{.N.IP}}{{else}}—{{end}}</div></div>
</div>

<h2>Проекты на этой ноде</h2>
{{if .NodeProjs}}
<div class="tablewrap">
<table>
<thead><tr><th>Проект</th><th>Состояние</th><th>Контейнер / процесс</th><th>Обновлено</th><th>Ошибка</th></tr></thead>
<tbody>
{{range .NodeProjs}}
<tr>
  <td><a href="/projects/{{.Project.ID}}" class="link-plain"><b>{{.Project.Name}}</b></a></td>
  <td><span class="badge b-{{.Inst.Status}}">{{if eq .Inst.Status "running"}}работает{{else if eq .Inst.Status "failed"}}ошибка{{else if eq .Inst.Status "starting"}}запускается{{else if eq .Inst.Status "orphan"}}нода была не в сети{{else if eq .Inst.Status "desired"}}в очереди{{else}}{{.Inst.Status}}{{end}}</span></td>
  <td class="sub mono-id">{{if .Inst.Container}}{{.Inst.Container}}{{else}}—{{end}}</td>
  <td class="sub">{{since .Inst.UpdatedAt}}</td>
  <td class="sub" style="color:var(--err)">{{.Inst.Error}}</td>
</tr>
{{end}}
</tbody></table>
</div>
{{else}}
<div class="empty">На этой ноде проектов нет. Новые проекты платформа распределит сюда автоматически (если нода включена и есть свободные ресурсы).</div>
{{end}}

<div class="grid2" style="margin-top:16px">
  <div class="panel">
    <h3>Ресурсы для платформы</h3>
    <p class="hint" style="margin:0 0 12px">Платформа не поставит на эту ноду проект, если он не влезет в лимиты. Ноль — без ограничения.</p>
    <form method="post" action="/api/action" onsubmit="return act(this,event)">
      <input type="hidden" name="__do" value="/nodes/limits"><input type="hidden" name="id" value="{{.N.ID}}">
      <div class="formgrid">
        <label class="fl">Память, МБ<input type="number" name="max_mem" value="{{.N.MaxMemMB}}" min="0" step="64"></label>
        <label class="fl">CPU, ядер<input type="number" name="max_cpus" value="{{printf "%.1f" .N.MaxCPUs}}" min="0" step="0.1"></label>
        <label class="fl">Диск, ГБ<input type="number" name="max_disk" value="{{.N.MaxDiskGB}}" min="0" step="1"></label>
        <label class="fl">Планировщик<select name="enabled">
          <option value="1" {{if .N.Enabled}}selected{{end}}>включён</option>
          <option value="0" {{if not .N.Enabled}}selected{{end}}>выключен</option>
        </select></label>
        <div><button class="btn" type="submit">Сохранить</button></div>
      </div>
    </form>
  </div>

  <div class="panel">
    <h3>Выполнить команду на ноде</h3>
    <form method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="3000" style="display:flex;gap:8px">
      <input type="hidden" name="__do" value="/nodes/exec"><input type="hidden" name="id" value="{{.N.ID}}">
      <input type="text" name="command" id="cmd-input" placeholder="команда ({{if eq .N.OS "windows"}}cmd /c{{else}}sh -c{{end}})" style="flex:1">
      <input type="number" name="timeout" value="30" min="1" max="600" style="width:74px" title="таймаут, сек">
      <button class="btn" type="submit">Выполнить</button>
    </form>
    <div class="palette">
    {{if eq .N.OS "windows"}}
      <button class="btn" onclick="qc('tasklist | findstr swag')">процессы агента</button>
      <button class="btn" onclick="qc('docker ps -a')">docker ps</button>
      <button class="btn" onclick="qc('dir C:\\ProgramData\\swagcore')">каталог платформы</button>
      <button class="btn" onclick="qc('powershell -c Get-Service swagcore-agent')">служба агента</button>
      <button class="btn" onclick="qc('systeminfo | findstr /C:&quot;Total Physical&quot;')">объём памяти</button>
      <button class="btn" onclick="qc('netstat -ano | findstr LISTENING')">слушающие порты</button>
      <button class="btn" onclick="qc('wmic os get LastBootUpTime')">когда была загрузка</button>
    {{else}}
      <button class="btn" onclick="qc('ps aux | grep -v grep | grep swag')">процессы агента</button>
      <button class="btn" onclick="qc('docker ps -a')">docker ps</button>
      <button class="btn" onclick="qc('df -h')">диски</button>
      <button class="btn" onclick="qc('uptime && free -m')">аптайм и память</button>
      <button class="btn" onclick="qc('ss -tlnp')">слушающие порты</button>
      <button class="btn" onclick="qc('systemctl status swagcore-agent --no-pager -l | head -20')">служба агента</button>
      <button class="btn" onclick="qc('docker stats --no-stream')">нагрузка контейнеров</button>
    {{end}}
    </div>
    <p class="hint">Результат команды появится в истории ниже.</p>
  </div>
</div>

<h2>Снимки экрана</h2>
<div id="shots">
{{if .Shots}}
<div class="shots">
{{range .Shots}}
<a href="/screenshots/{{.File}}" target="_blank" rel="noopener"><img src="/screenshots/{{.File}}" alt="снимок экрана" loading="lazy"><div class="cap">{{.File}}</div></a>
{{end}}
</div>
{{else}}
<div class="empty" id="shots-empty">Снимков пока нет. Нажмите «📷 Снимок экрана» — он появится здесь через несколько секунд.</div>
{{end}}
</div>

<h2>История команд</h2>
{{if .Results}}
<div class="tablewrap">
<table>
<thead><tr><th style="width:140px">Время</th><th style="width:100px">Тип</th><th>Команда</th><th style="width:70px">Итог</th><th>Вывод</th></tr></thead>
<tbody>
{{range .Results}}
<tr>
  <td class="sub mono-id">{{.CreatedAt.Format "02.01 15:04:05"}}</td>
  <td class="sub">{{.Kind}}{{if .Action}}<br>{{.Action}}{{end}}</td>
  <td class="mono-id">{{.Command}}</td>
  <td>{{if .OK}}<span class="badge b-ok nodot">ок</span>{{else}}<span class="badge b-failed nodot">сбой</span>{{end}}</td>
  <td><pre class="out">{{.Output}}{{if .Error}}<span style="color:var(--err)">{{.Error}}</span>{{end}}</pre></td>
</tr>
{{end}}
</tbody></table>
</div>
{{else}}
<div class="empty">Команд пока не выполнялось. Попробуйте одну из заготовок выше.</div>
{{end}}
<script>document.addEventListener('DOMContentLoaded',function(){pollShots({{.N.ID}});});</script>
{{template "foot" .}}{{end}}`

// ---------- Добавить ноду (главная фича) ----------
const tplAddNode = `{{define "addnode"}}{{template "head" .}}
<div class="pagehead">
  <div><h1>Подключить новую ноду</h1>
  <p class="sub">Одна команда на новой машине — и она станет нодой платформы. Скрипт сам всё скачает, поставит и настроит.</p></div>
</div>

{{if .NewToken}}
<div class="panel good">
  <h3 style="font-size:17px">✓ Токен создан. Осталось выполнить одну команду.</h3>
  <p class="sub">Нода «{{.NewName}}» появится в разделе <a href="/nodes">Ноды</a> через несколько секунд после запуска агента.</p>

  <fieldset style="margin-top:18px">
    <legend>Windows — PowerShell (обычный случай)</legend>
    <div style="display:flex;gap:10px;align-items:flex-start;margin-bottom:10px">
      <span class="stepnum">1</span>
      <div style="flex:1">
        <p class="sub" style="margin:0">На новой машине откройте <b>PowerShell</b> (Пуск → напечатать <span class="mono">powershell</span> → Enter).</p>
        <p class="hint">Права администратора скрипт запросит сам — просто согласитесь на запрос.</p>
      </div>
    </div>
    <div style="display:flex;gap:10px;align-items:flex-start;margin-bottom:14px">
      <span class="stepnum">2</span>
      <div style="flex:1">
        <p class="sub" style="margin:0 0 8px">Вставьте эту команду и нажмите Enter:</p>
        <div class="cmdblock">
          <code id="cmd-win">{{.CmdWin}}</code>
          <button class="btn" type="button" onclick="copyCode('cmd-win',this)">Скопировать</button>
        </div>
      </div>
    </div>
    <div style="display:flex;gap:10px;align-items:flex-start">
      <span class="stepnum">3</span>
      <div style="flex:1">
        <p class="sub" style="margin:0">Дождитесь строки <span class="mono" style="color:var(--ok)">ГОТОВО. Нода подключена.</span> и вернитесь сюда.</p>
      </div>
    </div>
  </fieldset>

  <fieldset>
    <legend>Linux — терминал (если нода на Ubuntu/Debian)</legend>
    <p class="sub" style="margin:0 0 8px">Выполните от root (через sudo):</p>
    <div class="cmdblock">
      <code id="cmd-linux">{{.CmdLinux}}</code>
      <button class="btn" type="button" onclick="copyCode('cmd-linux',this)">Скопировать</button>
    </div>
    <p class="hint">Установщик сам поставит Docker, если его нет, создаст systemd-службу и включит автозапуск.</p>
  </fieldset>

  <details>
    <summary class="hint" style="cursor:pointer">Вариант без токена в команде — скрипт спросит его при запуске</summary>
    <div class="cmdblock" style="margin-top:9px">
      <code id="cmd-win2">{{.CmdWinM}}</code>
      <button class="btn" type="button" onclick="copyCode('cmd-win2',this)">Скопировать</button>
    </div>
  </details>

  <div class="panel accent" style="margin:18px 0 0">
    <h3>Что произойдёт на новой машине</h3>
    <div class="grid2" style="margin-top:4px">
      <div>
        <p class="sub">1. Скрипт проверит систему и права, при необходимости запросит повышение до администратора.</p>
        <p class="sub">2. Скачает агента и проверит контрольную сумму файла.</p>
        <p class="sub">3. Поставит его как <b>службу</b>: автозапуск при включении + перезапуск при падении.</p>
      </div>
      <div>
        <p class="sub">4. Запустит агента и дождётся, пока нода появится в панели.</p>
        <p class="sub">5. Дальше платформа сама будет обновлять агента, когда выйдет новая сборка.</p>
        <p class="sub">6. Отключается одной командой: <span class="mono">.\install.ps1 -Uninstall</span></p>
      </div>
    </div>
  </div>

  <p style="margin-top:18px"><a href="/nodes"><button class="btn">Перейти к нодам →</button></a>
     <a href="/add-node"><button class="btn ghost">Создать ещё одну</button></a></p>
</div>
{{end}}

<div class="grid2">
  <div class="panel">
    <h3>Новая нода — создать токен</h3>
    <p class="hint" style="margin:0 0 14px">Токен — это ключ, по которому машина подключится к платформе. Создайте его, затем выполните команду на новой машине.</p>
    <form id="boot-form" method="post" action="/api/bootstrap">
      <div class="formgrid">
        <label class="fl" style="grid-column:1/-1">Имя машины
          <input type="text" name="name" id="boot-name" placeholder="напр. office-pc, home-laptop" maxlength="40">
        </label>
        <label class="fl">Память, МБ
          <input type="number" name="max_mem" value="0" min="0" step="64" placeholder="0 = без лимита">
        </label>
        <label class="fl">CPU, ядер
          <input type="number" name="max_cpus" value="0" min="0" step="0.1" placeholder="0 = без лимита">
        </label>
        <label class="fl">Диск, ГБ
          <input type="number" name="max_disk" value="0" min="0" step="1" placeholder="0 = без лимита">
        </label>
      </div>
      <div style="display:flex;gap:8px;align-items:center;margin-top:14px;flex-wrap:wrap">
        <button class="btn lg" type="submit">Создать токен и показать команду</button>
        <span class="hint" style="margin:0">Затем просто скопируйте команду и вставьте на новой машине.</span>
      </div>
    </form>
  </div>

  <div class="panel">
    <h3>Как это выглядит на практике</h3>
    <p class="sub"><b>Windows.</b> Открыли PowerShell, вставили одну строку, нажали Enter, согласились на запрос прав. Через полминуты нода в панели. Больше ничего делать не нужно — не нужны Python, Docker, Git или настройки.</p>
    <p class="sub"><b>Linux.</b> Та же команда через <span class="mono">sudo</span>. Установщик дополнительно поставит Docker, если его ещё нет.</p>
    <p class="sub"><b>Что дальше.</b> Нода сразу доступна для деплоя проектов: <a href="/projects#deploy">Проекты → Задеплоить</a>. Ничего больше настраивать не нужно.</p>
    <p class="hint">Ноды за натом (домашний ПК без белого IP) работают: агент сам устанавливает исходящее соединение, входящие порты не нужны.</p>
  </div>
</div>

<h2>Токены нод</h2>
{{if .Tokens}}
<div class="tablewrap">
<table>
<thead><tr><th>Имя</th><th>Токен</th><th>Состояние</th><th>Лимиты</th><th style="width:60px"></th></tr></thead>
<tbody>
{{range .Tokens}}
<tr>
  <td><b>{{.Name}}</b><br><span class="mono-id">создан {{.CreatedAt.Format "02.01.06 15:04"}}</span></td>
  <td class="mono-id" style="word-break:break-all;color:var(--dim)">{{.Token}}</td>
  <td>{{if .Used}}<span class="badge b-ok">занят: {{.UsedBy}}</span>{{else}}<span class="badge b-offline nodot">не использован</span>{{end}}</td>
  <td class="sub">{{if .MaxMemMB}}{{mb .MaxMemMB}}{{end}}{{if .MaxCPUs}} · {{printf "%.1f" .MaxCPUs}} CPU{{end}}{{if .MaxDiskGB}} · {{.MaxDiskGB}} ГБ{{end}}{{if not (or .MaxMemMB .MaxCPUs .MaxDiskGB)}}<span style="color:var(--dim)">без лимитов</span>{{end}}</td>
  <td><form class="inline" method="post" action="/api/action" onsubmit="return confirmAct(this,event,'Удалить токен {{.Name}}?')">
    <input type="hidden" name="__do" value="/nodes/delete-token"><input type="hidden" name="id" value="{{.ID}}">
    <button class="btn sm danger">✕</button></form></td>
</tr>
{{end}}
</tbody></table>
</div>
{{else}}
<div class="empty">Токенов пока нет. Создайте первый — форма слева.</div>
{{end}}
{{template "foot" .}}{{end}}`

// ---------- Проекты ----------
const tplProjects = `{{define "projects"}}{{template "head" .}}
<div class="pagehead">
  <div><h1>Проекты</h1><p class="sub">Что работает на ваших нодах. Платформа сама следит за процессами и перезапускает упавшее.</p></div>
  <div class="actions"><a href="#deploy"><button class="btn">＋ Задеплоить проект</button></a></div>
</div>

{{if .Rows}}
<div class="projcards">
{{$nodes := $.Nodes}}
{{range .Rows}}
<div class="projcard">
  <div class="phead">
    <div class="pname"><a href="/projects/{{.Project.ID}}">{{.Project.Name}}</a></div>
    <span class="badge b-{{.Project.Status}}">{{if eq .Project.Status "running"}}работает{{else if eq .Project.Status "failed"}}ошибка{{else if eq .Project.Status "starting"}}запускается{{else if eq .Project.Status "stopped"}}остановлен{{else}}желание: {{.Project.Status}}{{end}}</span>
  </div>
  {{if .Project.Manifest.Domain}}
  <p class="pline">🌐 <a href="https://{{.Project.Manifest.Domain}}" target="_blank" rel="noopener">{{.Project.Manifest.Domain}}</a></p>
  {{end}}
  {{if .Project.Error}}<p class="chips"><span class="chip err">{{.Project.Error}}</span></p>{{end}}
  <div class="chips">
    <span class="chip">{{if eq .Project.Manifest.Mode "process"}}нативный процесс{{else}}Docker{{end}}</span>
    {{if .Project.Manifest.Image}}<span class="chip">{{.Project.Manifest.Image}}</span>{{end}}
    {{if .Project.Manifest.Ports}}<span class="chip">порты: {{join .Project.Manifest.Ports ", "}}</span>{{end}}
    {{if .Project.Manifest.Resources.Memory}}<span class="chip">память {{.Project.Manifest.Resources.Memory}}</span>{{end}}
    <span class="chip">{{if eq .Project.Manifest.Placement "selected"}}только выбранные ноды{{else}}все подходящие ноды{{end}}</span>
    {{if .Project.Manifest.OS}}<span class="chip">только {{.Project.Manifest.OS}}</span>{{end}}
  </div>
  <div class="chips">
  {{$nodes := $.Nodes}}
  {{range .Instances}}
    {{$instNodeID := .NodeID}}
    {{$hn := ""}}{{range $nodes}}{{if eq .ID $instNodeID}}{{$hn = .Hostname}}{{end}}{{end}}
    <span class="badge b-{{.Status}}"{{if eq .Status "failed"}} title="{{.Error}}"{{end}}>
      {{if $hn}}<a style="color:inherit" href="/nodes/{{$instNodeID}}">{{$hn}}</a>{{else}}нода {{$instNodeID}}{{end}}: {{if eq .Status "running"}}работает{{else if eq .Status "failed"}}ошибка{{else if eq .Status "starting"}}запускается{{else if eq .Status "orphan"}}нода не в сети{{else}}{{.Status}}{{end}}
    </span>
  {{else}}
    <span class="chip warn">нет экземпляров — платформа ещё не разместила проект</span>
  {{end}}
  </div>
  <div class="nfoot">
    <span class="mono-id">обновлён {{since .Project.UpdatedAt}}</span>
    <div class="btnrow">
      <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="1500">
        <input type="hidden" name="__do" value="/projects/action"><input type="hidden" name="id" value="{{.Project.ID}}"><input type="hidden" name="action" value="start">
        <button class="btn sm">▶ Запустить</button></form>
      <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="1500">
        <input type="hidden" name="__do" value="/projects/action"><input type="hidden" name="id" value="{{.Project.ID}}"><input type="hidden" name="action" value="restart">
        <button class="btn sm ghost">↻ Перезапустить</button></form>
      <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="1500">
        <input type="hidden" name="__do" value="/projects/action"><input type="hidden" name="id" value="{{.Project.ID}}"><input type="hidden" name="action" value="stop">
        <button class="btn sm ghost">⏸ Остановить</button></form>
      <a href="/projects/{{.Project.ID}}"><button class="btn sm ghost">Логи</button></a>
      <form class="inline" method="post" action="/api/action" onsubmit="return confirmAct(this,event,'Удалить проект {{.Project.Name}}? Он будет остановлен на всех нодах, файлы проекта удалятся.')">
        <input type="hidden" name="__do" value="/projects/delete"><input type="hidden" name="id" value="{{.Project.ID}}">
        <button class="btn sm danger">✕</button></form>
    </div>
  </div>
</div>
{{end}}
</div>
{{else}}
<div class="panel">
  <h3>Проектов пока нет</h3>
  <p class="sub">Проект — это сайт, бот или скрипт. Вы описываете его манифестом (что запустить, на каком порту, какой домен), платформа сама размещает его на подходящей ноде и следит за работой.</p>
  <p style="margin-top:16px"><a href="#deploy"><button class="btn lg">Задеплоить первый проект</button></a></p>
</div>
{{end}}

<h2 id="deploy">Задеплоить новый проект</h2>
<div class="panel">
  <p class="hint" style="margin:0 0 12px">Вставьте манифест в YAML. Если проекта с таким именем ещё нет — он создастся; если есть — обновится и перезапустится.</p>
  <form method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="2000">
    <input type="hidden" name="__do" value="/projects/deploy">
    <textarea name="yaml" spellcheck="false">name: mysite
# ── Что запустить ──────────────────────────────────────────────
image: nginx:alpine           # Docker-образ
# ports: ["80:80"]            # порт, который видно снаружи
#
# ── Нативное приложение без Docker (mode: process) ─────────────
# mode: process
# os: windows                 # только Windows-ноды
# command: ./app.exe
# env: {PORT: "3001"}
#
# ── Ресурсы ───────────────────────────────────────────────────
resources:
  memory: 256m                # напр. 256m / 1g
  cpus: "0.5"                 # доля ядра
#
# ── Куда разместить ───────────────────────────────────────────
placement: all                # all = все подходящие ноды
# preferred_node: vm4168356   # или закрепить за конкретной нодой
#
# ── Публичный адрес ───────────────────────────────────────────
# domain: mysite.swag.best    # TLS *.swag.best настроен автоматически
#
# ── Файлы проекта ─────────────────────────────────────────────
# artifact: /artifacts/mysite/site.tar.gz
# artifact_sha: <sha256>
# mount_path: /usr/share/nginx/html</textarea>
    <div style="display:flex;gap:8px;margin-top:12px;align-items:center;flex-wrap:wrap">
      <button class="btn lg" type="submit">Задеплоить</button>
      <span class="hint" style="margin:0">Нужна хотя бы нода на связи — иначе проект дождётся её автоматически.</span>
    </div>
  </form>
</div>
{{template "foot" .}}{{end}}`

// ---------- Проект (детальная) ----------
const tplProject = `{{define "project"}}{{template "head" .}}
<div class="pagehead">
  <div>
    <h1>{{.P.Name}} <span class="badge b-{{.P.Status}}" style="vertical-align:middle">{{if eq .P.Status "running"}}работает{{else if eq .P.Status "failed"}}ошибка{{else if eq .P.Status "starting"}}запускается{{else if eq .P.Status "stopped"}}остановлен{{else}}{{.P.Status}}{{end}}</span></h1>
    <p class="sub">{{if eq .P.Manifest.Mode "process"}}нативный процесс{{else}}Docker-образ {{.P.Manifest.Image}}{{end}}
      · размещение: {{if eq .P.Manifest.Placement "selected"}}только выбранные ноды{{else}}все подходящие ноды{{end}}
      {{if .P.Manifest.OS}} · только ОС {{.P.Manifest.OS}}{{end}}</p>
    {{if .P.Manifest.Domain}}<p class="sub" style="margin-top:6px">🌐 <a href="https://{{.P.Manifest.Domain}}" target="_blank" rel="noopener">https://{{.P.Manifest.Domain}}</a></p>{{end}}
    {{if .P.Error}}<p style="color:var(--err);margin-top:8px">{{.P.Error}}</p>{{end}}
  </div>
  <div class="actions">
    <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="2500">
      <input type="hidden" name="__do" value="/projects/action"><input type="hidden" name="id" value="{{.P.ID}}"><input type="hidden" name="action" value="start">
      <button class="btn">▶ Запустить</button></form>
    <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="3500">
      <input type="hidden" name="__do" value="/projects/action"><input type="hidden" name="id" value="{{.P.ID}}"><input type="hidden" name="action" value="logs">
      <button class="btn ghost">Забрать логи</button></form>
    <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="2500">
      <input type="hidden" name="__do" value="/projects/action"><input type="hidden" name="id" value="{{.P.ID}}"><input type="hidden" name="action" value="restart">
      <button class="btn ghost">↻ Перезапустить</button></form>
    <form class="inline" method="post" action="/api/action" onsubmit="return act(this,event)" data-refresh="2500">
      <input type="hidden" name="__do" value="/projects/action"><input type="hidden" name="id" value="{{.P.ID}}"><input type="hidden" name="action" value="stop">
      <button class="btn ghost">⏸ Остановить</button></form>
  </div>
</div>

<h2>Где работает</h2>
{{if .Instances}}
<div class="tablewrap">
<table>
<thead><tr><th>Нода</th><th>Состояние</th><th>Контейнер / процесс</th><th>Обновлено</th><th>Сообщение агента</th></tr></thead>
<tbody>
{{range .Instances}}
{{$instNodeID := .NodeID}}{{$hn := ""}}{{range $.Nodes}}{{if eq .ID $instNodeID}}{{$hn = .Hostname}}{{end}}{{end}}
<tr>
  <td><a href="/nodes/{{$instNodeID}}" class="link-plain">{{if $hn}}{{$hn}}{{else}}нода {{$instNodeID}}{{end}}</a></td>
  <td><span class="badge b-{{.Status}}">{{if eq .Status "running"}}работает{{else if eq .Status "failed"}}ошибка{{else if eq .Status "starting"}}запускается{{else if eq .Status "orphan"}}нода не в сети{{else if eq .Status "stopped"}}остановлен{{else}}{{.Status}}{{end}}</span></td>
  <td class="sub mono-id">{{if .Container}}{{.Container}}{{else}}—{{end}}</td>
  <td class="sub">{{since .UpdatedAt}}</td>
  <td class="sub" style="color:var(--err)">{{.Error}}</td>
</tr>
{{end}}
</tbody></table>
</div>
{{else}}
<div class="empty">Проект ещё нигде не запущен. Нажмите «▶ Запустить» или измените манифест и задеплойте заново.</div>
{{end}}

<h2>Логи приложения <span class="mono-id" style="font-weight:400">(последние {{len .Logs}} строк)</span></h2>
{{if .Logs}}
<pre class="log">{{range .Logs}}{{.Data}}
{{end}}</pre>
{{else}}
<div class="empty">Логов пока нет. Нажмите «Забрать логи» — агент пришлёт последние строки вывода контейнера.</div>
{{end}}

<h2>Манифест</h2>
<div class="panel" style="padding:0">
<pre class="log" style="margin:0;border:none;border-radius:var(--r)">{{.ManifestYAML}}</pre>
</div>
<p class="hint">Чтобы изменить проект — откройте <a href="/projects#deploy">форму деплоя</a>, поправьте манифест и задеплойте снова: значения перезапишутся, проект перезапустится.</p>
{{template "foot" .}}{{end}}`

// ---------- События ----------
const tplEvents = `{{define "events"}}{{template "head" .}}
<div class="pagehead">
  <div><h1>События</h1><p class="sub">Хронология действий платформы: подключения нод, деплои, падения, исправления.</p></div>
</div>
{{if .Events}}
<div class="tablewrap">
<table>
<thead><tr><th style="width:155px">Время</th><th style="width:90px">Уровень</th><th style="width:170px">Источник</th><th>Что произошло</th></tr></thead>
<tbody>
{{range .Events}}
<tr>
  <td class="sub mono-id">{{.Time.Format "02.01.2006 15:04:05"}}</td>
  <td>{{if eq .Level "error"}}<span class="badge b-error">ошибка</span>{{else if eq .Level "warn"}}<span class="badge b-warn">внимание</span>{{else}}<span class="badge b-ok nodot">инфо</span>{{end}}</td>
  <td class="sub mono-id">{{.Source}}</td>
  <td>{{.Message}}</td>
</tr>
{{end}}
</tbody></table>
</div>
<p class="hint">Показаны последние {{len .Events}} значимых событий. Технический опрос нод (проверка раз в минуту) в журнал не пишется.
  База: {{mb .DBSize}} · WAL: {{mb .WALSize}} (сжимается автоматически).</p>
{{else}}
<div class="empty">Событий пока нет.</div>
{{end}}
{{template "foot" .}}{{end}}`

// ---------- Вход ----------
const tplLogin = `{{define "login"}}<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark">
<title>Вход · swagCore</title><link rel="icon" type="image/svg+xml" href="/favicon.svg">
<style>
:root{--bg:#0a0c11;--panel:#111520;--border:#252c3d;--text:#e9edf6;--muted:#8b95ad;--accent:#5b8cff;--err:#ff6b6b;--ok:#3ddc84}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:20px;
  background:radial-gradient(1100px 520px at 50% -12%,#1a2340 0%,var(--bg) 62%) no-repeat,var(--bg);
  color:var(--text);font:15px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
.box{width:100%;max-width:378px;background:var(--panel);border:1px solid var(--border);border-radius:16px;padding:32px}
h1{font-size:21px;margin:0 0 4px;letter-spacing:-.02em}
h1 b{font-weight:700}
.sub{color:var(--muted);font-size:13px;margin:0 0 22px}
label{display:block;font-size:12px;color:var(--muted);margin:0 0 6px;font-weight:500}
input{width:100%;background:#080a0e;color:var(--text);border:1px solid #333c52;border-radius:9px;padding:11px 13px;font-size:15px;font-family:inherit}
input:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 3px rgba(91,140,255,.15)}
button{width:100%;margin-top:16px;background:var(--accent);color:#fff;border:none;border-radius:9px;padding:12px;font-size:15px;font-weight:600;cursor:pointer;font-family:inherit}
button:hover{filter:brightness(1.12)}
.msg{padding:10px 13px;border-radius:9px;font-size:13.5px;margin-bottom:16px}
.msg.err{background:rgba(255,107,107,.11);border:1px solid rgba(255,107,107,.35);color:var(--err)}
.msg.warn{background:rgba(255,176,32,.11);border:1px solid rgba(255,176,32,.35);color:#ffb020}
.tip{margin-top:20px;padding-top:16px;border-top:1px solid var(--border);color:var(--muted);font-size:12.5px}
.tip code{background:#080a0e;border:1px solid var(--border);border-radius:5px;padding:2px 6px;font-size:12px;color:#a8d0ff}
</style></head>
<body><div class="box">
<h1><b>swag</b>Core</h1>
<p class="sub">Панель управления платформой</p>
{{if .Error}}
<div class="msg err">Неверный токен администратора.</div>
{{if eq .RateLimited true}}
<div class="msg warn">Слишком много попыток. Подождите минуту и повторите.</div>
{{end}}
{{end}}
<form method="post" action="/login">
<label for="t">Токен администратора</label>
<input id="t" type="password" name="admin_token" placeholder="вставьте токен" autofocus autocomplete="current-password" required>
<button type="submit">Войти</button>
</form>
<div class="tip">Токен администратора задаётся при развёртывании платформы и хранится на сервере. Если вы его не помните — переустановите или сбросьте платформу и посмотрите журнал установки.</div>
</div></body></html>{{end}}`

// ---------- ИИ-агент ----------
const tplAI = `{{define "ai"}}{{template "head" .}}
<div class="pagehead">
  <div><h1>ИИ-агент</h1><p class="sub">Опишите задачу словами — агент сам выполнит команды на нодах, задеплоит проекты и разберётся с логами.</p></div>
  <div class="actions"><form class="inline" onsubmit="return aiNewChat(this,event)"><button class="btn ghost">＋ Новый чат</button></form></div>
</div>
<div class="grid2" style="grid-template-columns:250px 1fr">
  <div class="panel" style="margin:0">
    <h3>Чаты</h3>
    <div id="chat-list"></div>
  </div>
  <div class="panel" style="margin:0;display:flex;flex-direction:column">
    <div id="chat-box" class="chat-box"><div class="chat-empty">Выберите чат слева или просто напишите задачу — агент создаст чат сам.</div></div>
    <form id="ai-form" class="chat-input" onsubmit="return aiSend(this,event)">
      <textarea name="text" id="ai-text" rows="1" placeholder="Например: «покажи свободную память на нодах», «задеплой nginx с доменом test.swag.best», «почему упал проект X»" autocomplete="off"></textarea>
      <button id="ai-send-btn" type="submit" title="Отправить (Enter)">➤</button>
    </form>
    <div class="hint">Enter — отправить · Shift+Enter — новая строка. Агент работает в фоне: вкладку можно закрыть.</div>
  </div>
</div>
<style>
.chat-box{flex:1;min-height:44vh;max-height:58vh;overflow-y:auto;padding:14px;border:1px solid var(--border);
  border-radius:var(--r2);margin-bottom:10px;display:flex;flex-direction:column;gap:12px;background:#080a0e}
.chat-empty{margin:auto;color:var(--dim);font-size:13px;text-align:center;max-width:420px}
.chat-list button{display:block;width:100%;text-align:left;margin:3px 0;padding:9px 12px;background:none;
  border:1px solid var(--border);border-radius:8px;color:var(--text);cursor:pointer;font-size:13px;
  font-family:inherit;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.chat-list button:hover{border-color:var(--border2)}
.chat-list button.sel{border-color:var(--accent);background:var(--panel2)}
.msg{max-width:82%;padding:10px 14px;border-radius:14px;font-size:13.5px;line-height:1.55;white-space:pre-wrap;word-break:break-word}
.msg.user{align-self:flex-end;background:linear-gradient(135deg,#1e4d80,#163a60);border:1px solid #2a5b8a}
.msg.assistant{align-self:flex-start;background:var(--panel2);border:1px solid var(--border)}
.msg .who{display:block;font-size:10.5px;text-transform:uppercase;letter-spacing:.07em;color:var(--dim);margin-bottom:5px}
.msg .tools{display:block;margin-top:8px;padding-top:7px;border-top:1px dashed var(--border);color:#7ea6d9;font-size:11.5px}
.chat-input{display:flex;gap:8px;align-items:flex-end}
.chat-input textarea{flex:1;resize:none;min-height:44px;max-height:150px;font-family:inherit;font-size:13.5px;line-height:1.45}
.chat-input button{height:44px;width:54px;font-size:17px;border-radius:var(--r2);flex:0 0 auto}
.chat-input button:disabled{opacity:.5;cursor:not-allowed}
.typing{align-self:flex-start;color:var(--muted);font-size:12px;font-style:italic;padding:4px 8px}
.typing::after{content:'…';animation:blink 1.2s infinite}
@keyframes blink{50%{opacity:.2}}
</style>
<script>
let curChat = 0, pollT = null, sending = false;
async function jget(u){ const r = await fetch(u); return r.json(); }
async function jpost(u, body){ const r = await fetch(u, {method:'POST', body}); return r.json(); }
function esc(s){ const d = document.createElement('div'); d.textContent = s||''; return d.innerHTML; }
function renderMsg(m){
  const el = document.createElement('div');
  el.className = 'msg ' + (m.role === 'user' ? 'user' : 'assistant');
  let html = '<span class="who">' + (m.role === 'user' ? 'Вы' : 'Агент') + '</span>' + esc(m.content);
  if (m.tools){ try{ const steps = JSON.parse(m.tools);
    if (Array.isArray(steps) && steps.length) html += '<span class="tools">выполнено действий: ' + steps.length + ' — ' + esc(steps.join(' · ')) + '</span>';
  }catch(e){} }
  el.innerHTML = html; return el;
}
async function loadChats(){
  const j = await jget('/api/ai/chats');
  const box = document.getElementById('chat-list'); box.innerHTML = '';
  for (const c of (j.chats||[])){
    const b = document.createElement('button');
    b.textContent = c.title || ('чат #' + c.id);
    if (c.id === curChat) b.classList.add('sel');
    b.onclick = ()=>{ curChat = c.id; loadChats(); loadMsgs(); };
    box.appendChild(b);
  }
  if (!curChat && (j.chats||[]).length){ curChat = j.chats[0].id; loadChats(); loadMsgs(); }
}
async function loadMsgs(){
  if (!curChat) return;
  const j = await jget('/api/ai/messages?chat_id=' + curChat);
  const box = document.getElementById('chat-box');
  const stick = box.scrollHeight - box.scrollTop - box.clientHeight < 70;
  box.innerHTML = '';
  for (const m of (j.messages||[])) if (m.role !== 'tool') box.appendChild(renderMsg(m));
  if (j.busy){
    const t = document.createElement('div'); t.className='typing'; t.textContent='агент работает, выполняет действия';
    box.appendChild(t);
    document.getElementById('ai-send-btn').disabled = true;
    if (!pollT) pollT = setInterval(loadMsgs, 3500);
  } else {
    document.getElementById('ai-send-btn').disabled = false;
    if (pollT){ clearInterval(pollT); pollT = null; }
  }
  if (stick) box.scrollTop = box.scrollHeight;
}
function aiNewChat(f, ev){
  ev.preventDefault();
  jpost('/api/ai/chats', new FormData(f)).then(j=>{ if (j.ok){ curChat = j.id; loadChats(); loadMsgs(); } });
  return false;
}
async function aiSend(f, ev){
  ev.preventDefault();
  if (sending) return false;
  const inp = document.getElementById('ai-text');
  const text = inp.value.trim();
  if (!text){ inp.focus(); return false; }
  sending = true; inp.value=''; autosize(inp);
  const box = document.getElementById('chat-box');
  const emp = box.querySelector('.chat-empty'); if (emp) emp.remove();
  box.appendChild(renderMsg({role:'user', content:text}));
  box.scrollTop = box.scrollHeight;
  const fd = new FormData(); fd.append('chat_id', curChat); fd.append('text', text);
  try {
    const j = await jpost('/api/ai/send', fd);
    if (!j.ok){ toast(j.error || 'ошибка', true); inp.value = text; autosize(inp); }
    else { curChat = j.chat_id; loadChats(); loadMsgs(); }
  } finally { sending = false; }
  return false;
}
function autosize(t){ t.style.height='auto'; t.style.height=Math.min(t.scrollHeight,150)+'px'; }
document.addEventListener('DOMContentLoaded', () => {
  const t = document.getElementById('ai-text');
  t.addEventListener('input', ()=>autosize(t));
  t.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.shiftKey){ e.preventDefault(); document.getElementById('ai-form').requestSubmit(); }
  });
});
loadChats();
</script>
{{template "foot" .}}{{end}}`

// ---------- подвал + JS ----------
const tplFoot = `{{define "foot"}}</main></div>
<div class="toast" id="toast"></div>
<script>
// ---------- тост ----------
let toastT;
function toast(msg, err){
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.classList.toggle('err', !!err);
  t.classList.add('show');
  clearTimeout(toastT);
  toastT = setTimeout(()=>t.classList.remove('show'), err ? 6500 : 2800);
}

// ---------- копирование блока кода ----------
function copyCode(id, btn){
  const txt = document.getElementById(id).innerText.trim();
  const done = () => { const o = btn.innerText; btn.innerText='Скопировано ✓'; setTimeout(()=>btn.innerText=o,1600); };
  if (navigator.clipboard && window.isSecureContext){
    navigator.clipboard.writeText(txt).then(done, () => fallbackCopy(txt, done));
  } else fallbackCopy(txt, done);
  return false;
}
function fallbackCopy(txt, done){
  const ta = document.createElement('textarea');
  ta.value = txt; ta.style.position='fixed'; ta.style.opacity='0';
  document.body.appendChild(ta); ta.select();
  try { document.execCommand('copy'); done(); } catch(e){ toast('не удалось скопировать — выделите текст вручную', true); }
  document.body.removeChild(ta);
}

// ---------- AJAX для всех action-форм (страница не перезагружается) ----------
async function act(form, ev){
  if (ev) ev.preventDefault();
  const btn = form.querySelector('button[type=submit],button');
  const old = btn ? btn.innerText : '';
  if (btn){ btn.disabled = true; btn.innerText = '…'; }
  try{
    const r = await fetch('/api/action', {method:'POST', body:new FormData(form)});
    let j = {}; try{ j = await r.json(); }catch(e){}
    if (r.status === 401){ toast('Сессия истекла — войдите заново', true); setTimeout(()=>location.href='/login',1200); return false; }
    if (!r.ok || j.ok === false){ toast(j.error || ('ошибка ' + r.status), true); }
    else {
      toast(j.message || 'Готово');
      const d = parseInt(form.dataset.refresh||'0');
      if (d > 0) setTimeout(()=>location.reload(), d);
    }
  }catch(e){ toast('Сеть недоступна: ' + e, true); }
  if (btn){ btn.disabled = false; btn.innerText = old; }
  return false;
}

// ---------- подтверждение + действие ----------
function confirmAct(form, ev, question){
  if (!confirm(question)) return false;
  return act(form, ev);
}

// ---------- удаление ноды ----------
function delNode(form, ev, name, status){
  if (!confirm('Убрать ноду «' + name + '» из платформы?')) return false;
  if (status === 'online')
    if (!confirm('Нода «' + name + '» СЕЙЧАС НА СВЯЗИ.\n\n' +
                 'Агент на самой машине продолжит работать — его нужно остановить отдельно ' +
                 '(«Отключить совсем» в install.ps1 или вручную).\n\nВсё равно убрать ноду из платформы?')) return false;
  return act(form, ev);
}

// ---------- быстрые команды ----------
function qc(cmd){
  const inp = document.getElementById('cmd-input');
  if (!inp) return;
  inp.value = cmd; inp.focus();
  const f = inp.closest('form');
  if (f) act(f, new Event('submit'));
}

// ---------- создание токена + показ команды ----------
const bootForm = document.getElementById('boot-form');
if (bootForm){
  bootForm.addEventListener('submit', async function(ev){
    ev.preventDefault();
    const btn = bootForm.querySelector('button[type=submit]');
    const old = btn.innerText; btn.disabled = true; btn.innerText = 'Создаю…';
    try{
      const r = await fetch('/api/bootstrap', {method:'POST', body:new FormData(bootForm)});
      const j = await r.json();
      if (!r.ok || !j.ok){ toast(j.error || 'ошибка ' + r.status, true); return; }
      const name = (bootForm.querySelector('#boot-name').value || '').trim() || j.name;
      location.href = '/add-node?token=' + encodeURIComponent(j.token) + '&name=' + encodeURIComponent(name);
    }catch(e){ toast('Сеть недоступна: ' + e, true); }
    finally{ btn.disabled = false; btn.innerText = old; }
  });
}

// ---------- живые скриншоты ----------
function pollShots(nodeId){
  const root = document.getElementById('shots');
  if (!root) return;
  let box = root.querySelector('.shots');
  if (!box){
    const empty = root.querySelector('.empty');
    if (empty) empty.remove();
    box = document.createElement('div'); box.className = 'shots'; root.appendChild(box);
  }
  const known = new Set([...box.querySelectorAll('img')].map(i => i.src.split('/').pop()));
  let tries = 0;
  const iv = setInterval(async () => {
    if (++tries > 40){ clearInterval(iv); return; }
    try{
      const r = await fetch('/api/screenshots?node_id=' + nodeId + '&limit=8');
      if (!r.ok) return;
      const j = await r.json();
      let added = 0;
      for (const f of (j.files||[])){
        if (known.has(f.file)) continue;
        known.add(f.file);
        const a = document.createElement('a');
        a.href = '/screenshots/' + f.file; a.target = '_blank'; a.rel = 'noopener';
        const img = document.createElement('img'); img.src = '/screenshots/' + f.file; img.loading = 'lazy';
        const cap = document.createElement('div'); cap.className = 'cap'; cap.textContent = f.file;
        a.appendChild(img); a.appendChild(cap); box.prepend(a); added++;
      }
      if (added) toast('Получен новый снимок экрана');
    }catch(e){}
  }, 4000);
}
</script>
</body></html>{{end}}`
