/* ============ PyMLOps — app.js ============ */
(function(){
'use strict';
var $  = function(s, r){ return (r || document).querySelector(s); };
var $$ = function(s, r){ return Array.prototype.slice.call((r || document).querySelectorAll(s)); };

/* ---------------- course structure ---------------- */
var COURSE = [
  { section:'Python', icon:'🐍', topics:[
    ['py-setup','Setup & Variables','install venv variables f-string typing basics repl'],
    ['py-types','Core Data Structures','list tuple dict set data structures'],
    ['py-flow','Control Flow','if else for while match break continue loops indentation'],
    ['py-funcs','Functions','def args kwargs lambda default type hints docstring'],
    ['py-comp','Comprehensions & Generators','list dict set generator yield lazy memory'],
    ['py-oop','Classes & OOP','class object init inheritance super dataclass property'],
    ['py-errors','Errors, Files & Modules','try except raise traceback open json pathlib import main'],
    ['py-ds','Python for Data & ML','numpy pandas scikit-learn dataframe train test split fit predict']
  ]},
  { section:'MLOps', icon:'⚙️', topics:[
    ['ml-what','What is MLOps?','lifecycle devops maturity continuous training levels drift story'],
    ['ml-env','Environments & Dependencies','venv pip conda pyproject poetry uv pinning versions'],
    ['ml-git','Git for ML Projects','git version control branch commit merge gitignore save point'],
    ['ml-track','Experiment Tracking (MLflow)','mlflow wandb params metrics artifacts registry runs'],
    ['ml-dvc','Data & Model Versioning (DVC)','dvc data model versioning pipeline reproducibility pointer'],
    ['ml-docker','Docker for ML','docker container image dockerfile build run lunchbox layers'],
    ['ml-serve','Serving Models (FastAPI)','fastapi uvicorn api endpoint pydantic json serve'],
    ['ml-cicd','CI/CD for ML','github actions ci cd tests deploy canary blue-green shadow workflow'],
    ['ml-orchestrate','Orchestration (Airflow)','airflow dag scheduler prefect dagster kubeflow retries'],
    ['ml-monitor','Monitoring & Drift','drift data concept evidently prometheus grafana retraining']
  ]},
  { section:'Labs', icon:'🧪', topics:[
    ['lab-0','Lab 0 — Machine Setup','install python git venv folders environment'],
    ['lab-1','Lab 1 — Python Workout','practice script basics exercise run'],
    ['lab-2','Lab 2 — Train Your First Model','sklearn random forest train dataset prepare'],
    ['lab-3','Lab 3 — Version Control with Git','git init commit branch merge github push'],
    ['lab-4','Lab 4 — Track Experiments (MLflow)','mlflow ui runs params metrics log'],
    ['lab-5','Lab 5 — Version Data (DVC)','dvc add push pull remote pointer'],
    ['lab-6','Lab 6 — Serve the Model (FastAPI)','fastapi uvicorn predict health docs api'],
    ['lab-7','Lab 7 — Ship It in Docker','dockerfile build run container image'],
    ['lab-8','Lab 8 — Automate with CI','pytest ruff github actions workflow tests'],
    ['lab-9','Lab 9 (Bonus) — Drift Report','evidently drift report monitoring']
  ]},
  { section:'Cheatsheets', icon:'📋', topics:[
    ['cs-python','Python','python quick syntax snippets'],
    ['cs-env','pip · venv · conda','environment dependency commands'],
    ['cs-git','Git','git commands reference'],
    ['cs-docker','Docker','docker commands reference'],
    ['cs-mlflow','MLflow','mlflow commands api reference'],
    ['cs-sklearn','pandas · scikit-learn','pandas sklearn commands reference'],
    ['cs-linux','Shell & Utils','bash terminal linux commands reference']
  ]}
];

var ALL = [];
COURSE.forEach(function(sec){
  sec.topics.forEach(function(t, i){
    ALL.push({ id:t[0], title:t[1], kw:t[2] || '', section:sec.section, idx:i, sec:sec });
  });
});
function find(id){ for (var i = 0; i < ALL.length; i++) if (ALL[i].id === id) return ALL[i]; return null; }

/* ---------------- syntax highlighting ---------------- */
var LANGS = {
  python: [
    [String.raw`#[^\n]*`, 'com'],
    [String.raw`"""[\s\S]*?"""|'''[\s\S]*?'''|"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'`, 'str'],
    [String.raw`@[A-Za-z_][\w.]*`, 'dec'],
    [String.raw`\b(?:False|None|True|and|as|assert|async|await|break|class|continue|def|del|elif|else|except|finally|for|from|global|if|import|in|is|lambda|match|case|nonlocal|not|or|pass|raise|return|try|while|with|yield)\b`, 'kw'],
    [String.raw`\b(?:print|len|range|type|int|str|float|bool|list|dict|set|tuple|open|isinstance|enumerate|zip|sorted|reversed|sum|min|max|abs|round|map|filter|any|all|super|self|next)\b`, 'bi'],
    [String.raw`\b\d[\d_]*(?:\.\d+)?\b`, 'num']
  ],
  bash: [
    [String.raw`#[^\n]*`, 'com'],
    [String.raw`"(?:\\.|[^"\\\n])*"|'[^'\n]*'`, 'str'],
    [String.raw`(?:^|\s)--?[A-Za-z][\w-]*`, 'dec'],
    [String.raw`\b(?:if|then|else|elif|fi|for|in|do|done|while|case|esac|function|export|source|return|local)\b`, 'kw'],
    [String.raw`\$\{[^}]*\}|\$\w+`, 'bi'],
    [String.raw`\b\d+\b`, 'num']
  ],
  yaml: [
    [String.raw`#[^\n]*`, 'com'],
    [String.raw`"(?:\\.|[^"\\\n])*"|'[^'\n]*'`, 'str'],
    [String.raw`^\s*-?\s*[\w.-]+(?=:)`, 'key'],
    [String.raw`\b(?:true|false|null|True|False)\b`, 'kw'],
    [String.raw`\b\d+(?:\.\d+)?\b`, 'num']
  ],
  docker: [
    [String.raw`#[^\n]*`, 'com'],
    [String.raw`^\s*(?:FROM|RUN|CMD|COPY|ADD|WORKDIR|EXPOSE|ENV|ENTRYPOINT|ARG|LABEL|USER|VOLUME)\b`, 'kw'],
    [String.raw`"(?:\\.|[^"\\\n])*"`, 'str'],
    [String.raw`\b\d+\b`, 'num']
  ],
  toml: [
    [String.raw`#[^\n]*`, 'com'],
    [String.raw`^\s*\[[^\]\n]*\]`, 'kw'],
    [String.raw`"(?:\\.|[^"\\\n])*"|'[^'\n]*'`, 'str'],
    [String.raw`^\s*[\w."-]+(?=\s*=)`, 'key'],
    [String.raw`\b\d+(?:\.\d+)?\b`, 'num']
  ],
  json: [
    [String.raw`"(?:\\.|[^"\\\n])*"(?=\s*:)`, 'key'],
    [String.raw`"(?:\\.|[^"\\\n])*"`, 'str'],
    [String.raw`\b\d+(?:\.\d+)?\b`, 'num'],
    [String.raw`\b(?:true|false|null)\b`, 'kw']
  ],
  text: []
};

function esc(s){
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
function hl(src, lang){
  var rules = LANGS[lang] || [];
  if (!rules.length) return esc(src);
  var master = new RegExp(rules.map(function(r){ return '(' + r[0] + ')'; }).join('|'), 'gm');
  var out = '', last = 0, m;
  while ((m = master.exec(src)) !== null){
    if (m.index > last) out += esc(src.slice(last, m.index));
    for (var i = 0; i < rules.length; i++){
      if (m[i + 1] !== undefined){
        out += '<span class="tk-' + rules[i][1] + '">' + esc(m[i + 1]) + '</span>';
        break;
      }
    }
    last = m.index + m[0].length;
    if (m[0].length === 0) master.lastIndex++;
  }
  if (last < src.length) out += esc(src.slice(last));
  return out;
}

/* ---------------- progress (localStorage) ---------------- */
var KEY = 'pymlops.done.v1';
var done = new Set();
try { done = new Set(JSON.parse(localStorage.getItem(KEY) || '[]')); } catch (e) {}
var current = 'home';

function updateProgress(){
  var n = done.size, total = ALL.length;
  $('#progressFill').style.width = (total ? (n / total * 100) : 0) + '%';
  $('#progressLabel').textContent = n + '/' + total;
  $$('[data-check]').forEach(function(c){
    var isDone = done.has(c.getAttribute('data-check'));
    c.textContent = isDone ? '✓' : '○';
    c.classList.toggle('is-done', isDone);
  });
  var t = find(current);
  var btn = $('#doneBtn');
  if (t){
    btn.style.display = '';
    btn.classList.toggle('completed', done.has(t.id));
    btn.textContent = done.has(t.id) ? '✓ Completed' : '✓ Mark complete';
  } else {
    btn.style.display = 'none';
  }
}
function saveDone(){
  try { localStorage.setItem(KEY, JSON.stringify(Array.from(done))); } catch (e) {}
  updateProgress();
}

/* ---------------- routing ---------------- */
function showTopic(id, push){
  var el = document.getElementById(id);
  if (!el) return;
  current = id;
  $$('.topic').forEach(function(a){ a.classList.toggle('active', a === el); });
  var meta = find(id);
  if (meta){
    var crumb = $('.crumb', el);
    if (crumb){
      var label = meta.section === 'Labs' ? 'lab'
                : meta.section === 'Cheatsheets' ? 'reference' : 'topic';
      crumb.textContent = meta.section + ' — ' + label + ' ' + (meta.idx + 1) + ' / ' + meta.sec.topics.length;
    }
  }
  var i = ALL.findIndex(function(t){ return t.id === id; });
  var prev = (i > 0) ? ALL[i - 1] : null;
  var next = null;
  if (i === -1 && ALL.length) next = ALL[0];
  else if (i > -1 && i < ALL.length - 1) next = ALL[i + 1];
  $('#prevBtn').style.visibility = prev ? 'visible' : 'hidden';
  $('#prevTitle').textContent = prev ? prev.title : '';
  $('#nextBtn').style.visibility = next ? 'visible' : 'hidden';
  $('#nextTitle').textContent = next ? next.title : '';
  if (push !== false){
    if (history.replaceState) history.replaceState(null, '', '#' + id);
  }
  window.scrollTo(0, 0);
  updateProgress();
}

/* ---------------- dropdowns + outline ---------------- */
function buildNav(){
  var nav = $('#navDds');
  var home = document.createElement('button');
  home.className = 'dd-btn';
  home.setAttribute('data-go', 'home');
  home.textContent = '🏠 Home';
  nav.appendChild(home);

  COURSE.forEach(function(sec){
    var dd = document.createElement('div');
    dd.className = 'dd';
    var items = sec.topics.map(function(t, i){
      return '<div class="dd-item" data-go="' + t[0] + '">' +
             '<span class="num">' + (i + 1) + '</span><span>' + t[1] + '</span>' +
             '<span class="check" data-check="' + t[0] + '">○</span></div>';
    }).join('');
    dd.innerHTML =
      '<button class="dd-btn" aria-haspopup="true" aria-expanded="false">' +
        sec.icon + ' ' + sec.section + ' <span class="caret">▾</span></button>' +
      '<div class="dd-menu"><div class="dd-sec">' + sec.icon + ' ' + sec.section + '</div>' + items + '</div>';
    nav.appendChild(dd);
  });

  $$('.dd-btn', nav).forEach(function(btn){
    if (btn.parentElement.classList.contains('dd')){
      btn.addEventListener('click', function(e){
        e.stopPropagation();
        var dd = btn.parentElement;
        var wasOpen = dd.classList.contains('open');
        closeMenus();
        if (!wasOpen){
          dd.classList.add('open');
          btn.setAttribute('aria-expanded', 'true');
        }
      });
    }
  });
}
function buildOutline(){
  var outline = $('#outline');
  COURSE.forEach(function(sec){
    var card = document.createElement('div');
    card.className = 'outline-card';
    var lis = sec.topics.map(function(t){
      return '<li><button class="outline-item" data-go="' + t[0] + '">' +
             '<span>' + t[1] + '</span>' +
             '<span class="check" data-check="' + t[0] + '">○</span></button></li>';
    }).join('');
    card.innerHTML = '<h3>' + sec.icon + ' ' + sec.section + '</h3><ul style="list-style:none;padding:0;margin:0">' + lis + '</ul>';
    outline.appendChild(card);
  });
  var stats = $('#trackStats');
  if (stats) stats.textContent = ALL.length + ' topics across ' + COURSE.length + ' tracks — checkmarks show what you have finished.';
}
function closeMenus(){
  $$('.dd.open').forEach(function(d){
    d.classList.remove('open');
    var b = $('.dd-btn', d);
    if (b) b.setAttribute('aria-expanded', 'false');
  });
  $('#searchResults').classList.remove('open');
}

/* ---------------- search ---------------- */
function setupSearch(){
  var input = $('#search'), box = $('#searchResults');
  input.addEventListener('input', function(){
    var q = input.value.trim().toLowerCase();
    if (q.length < 2){ box.classList.remove('open'); box.innerHTML = ''; return; }
    var hits = ALL.filter(function(t){
      return (t.title + ' ' + t.kw + ' ' + t.section).toLowerCase().indexOf(q) > -1;
    });
    box.innerHTML = hits.length
      ? hits.map(function(t){
          return '<div class="dd-item" data-go="' + t.id + '"><span>' + t.title +
                 '</span><span class="sec-tag">' + t.section + '</span></div>';
        }).join('')
      : '<div class="dd-empty">No matching topics.</div>';
    box.classList.add('open');
  });
  input.addEventListener('keydown', function(e){
    if (e.key === 'Enter'){
      var first = $('.dd-item', box);
      if (first){ showTopic(first.getAttribute('data-go')); input.value = ''; closeMenus(); input.blur(); }
    }
  });
}

/* ---------------- clipboard ---------------- */
function legacyCopy(text){
  var ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  try { document.execCommand('copy'); } catch (e) {}
  document.body.removeChild(ta);
}
function copyText(text, cb){
  if (navigator.clipboard && navigator.clipboard.writeText){
    navigator.clipboard.writeText(text).then(cb, function(){ legacyCopy(text); cb(); });
  } else {
    legacyCopy(text);
    cb();
  }
}

/* ---------------- copy buttons ---------------- */
function addCopyButtons(){
  $$('.codebox').forEach(function(box){
    var code = box.querySelector('code');
    var head = box.querySelector('.codebox-head');
    if (!code || !head) return;
    var btn = document.createElement('button');
    btn.className = 'copy-btn';
    btn.textContent = '⧉ copy';
    btn.addEventListener('click', function(){
      copyText(code.innerText, function(){
        btn.textContent = '✓ copied';
        setTimeout(function(){ btn.textContent = '⧉ copy'; }, 1300);
      });
    });
    head.appendChild(btn);
  });
  $$('.cs-row').forEach(function(row){
    var code = row.querySelector('code');
    if (!code) return;
    var btn = document.createElement('button');
    btn.className = 'cs-copy';
    btn.title = 'Copy';
    btn.textContent = '⧉';
    btn.addEventListener('click', function(){
      copyText(code.innerText, function(){
        btn.textContent = '✓';
        setTimeout(function(){ btn.textContent = '⧉'; }, 1300);
      });
    });
    row.appendChild(btn);
  });
}

/* ---------------- theme ---------------- */
function setupTheme(){
  var btn = $('#themeBtn');
  function setTheme(t){
    document.documentElement.setAttribute('data-theme', t);
    try { localStorage.setItem('pymlops.theme', t); } catch (e) {}
    btn.textContent = (t === 'dark') ? '☀️' : '🌙';
  }
  var saved = 'dark';
  try { saved = localStorage.getItem('pymlops.theme') || 'dark'; } catch (e) {}
  setTheme(saved);
  btn.addEventListener('click', function(){
    setTheme(document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark');
  });
}

/* ---------------- wire up + init ---------------- */
buildNav();
buildOutline();
setupSearch();
setupTheme();

/* highlight all code */
$$('pre code').forEach(function(el){
  var cls = el.className.split(/\s+/).filter(function(c){ return c.indexOf('lang-') === 0; })[0];
  el.innerHTML = hl(el.textContent, cls ? cls.slice(5) : 'text');
});
addCopyButtons();

/* delegated navigation */
document.addEventListener('click', function(e){
  var go = e.target.closest ? e.target.closest('[data-go]') : null;
  if (go){ showTopic(go.getAttribute('data-go')); closeMenus(); return; }
  if (!e.target.closest('.dd') && !e.target.closest('.search-wrap')) closeMenus();
});
document.addEventListener('keydown', function(e){
  if (e.key === 'Escape') closeMenus();
  if (e.key === '/' && !/INPUT|TEXTAREA/.test(document.activeElement.tagName)){
    e.preventDefault();
    $('#search').focus();
  }
});

$('#prevBtn').addEventListener('click', function(){
  var i = ALL.findIndex(function(t){ return t.id === current; });
  if (i > 0) showTopic(ALL[i - 1].id);
});
$('#nextBtn').addEventListener('click', function(){
  var i = ALL.findIndex(function(t){ return t.id === current; });
  if (i === -1 && ALL.length) showTopic(ALL[0].id);
  else if (i > -1 && i < ALL.length - 1) showTopic(ALL[i + 1].id);
});
$('#doneBtn').addEventListener('click', function(){
  var t = find(current);
  if (!t) return;
  if (done.has(t.id)) done.delete(t.id); else done.add(t.id);
  saveDone();
});
window.addEventListener('hashchange', function(){
  var id = location.hash.slice(1);
  if (id && id !== current && document.getElementById(id)) showTopic(id, false);
});

var initial = location.hash.slice(1);
showTopic(document.getElementById(initial) ? initial : 'home', false);
})();
