// Rapport vigie : trace les graphs uPlot à partir des données en ligne. Aucune requête réseau.
(function () {
  'use strict';
  var data = {};
  try { data = JSON.parse(document.getElementById('vigie-data').textContent || '{}'); } catch (e) { data = {}; }
  var nf = new Intl.NumberFormat('fr-FR', { maximumFractionDigits: 2 });
  var nf0 = new Intl.NumberFormat('fr-FR', { maximumFractionDigits: 0 });
  function fmtMs(v) { return v == null ? '—' : (v < 10 ? nf.format(v) : nf0.format(v)); }
  var axis = { stroke: '#6b7280', grid: { stroke: '#e5e7eb', width: 1 }, ticks: { stroke: '#e5e7eb', width: 1 }, font: '11px system-ui, sans-serif', labelFont: '600 11.5px system-ui, sans-serif' };
  function ax(o) { var r = {}; for (var k in axis) r[k] = axis[k]; for (var k2 in o) r[k2] = o[k2]; return r; }

  function draw(box, c) {
    if (!c || !c.xs || !c.xs.length || typeof uPlot === 'undefined') { box.textContent = 'Graph indisponible.'; return; }
    var series = [{ label: c.type === 'hist' ? 'latence (ms)' : 's', value: function (u, v) { return v == null ? '—' : (c.type === 'hist' ? fmtMs(v) + ' ms' : nf0.format(v) + ' s'); } }];
    var rows = [c.xs];
    c.series.forEach(function (s) {
      series.push({ label: s.label, stroke: s.color, width: 2, dash: s.dash ? [6, 4] : undefined, spanGaps: false,
        fill: c.type === 'hist' && !s.dash ? s.color + '22' : undefined,
        points: { show: false },
        value: function (u, v) { return v == null ? '—' : (c.type === 'hist' ? nf.format(v) + ' %' : nf0.format(v) + ' Mo/s'); } });
      rows.push(s.values);
    });
    var opts = {
      width: Math.min(680, box.clientWidth || 680), height: c.height || 230,
      scales: { x: c.logx ? { time: false, distr: 3, log: 10 } : { time: false }, y: { range: function (u, mn, mx) { return [0, mx > 0 ? mx * 1.08 : 1]; } } },
      axes: [ax({ label: c.x, values: c.logx ? function (u, vals) { return vals.map(function (v) { return v == null ? '' : fmtMs(v); }); } : undefined }), ax({ label: c.y, size: 56 })],
      series: series,
      legend: { live: true },
      cursor: { drag: { x: false, y: false } }
    };
    new uPlot(opts, rows, box);
  }

  var boxes = document.querySelectorAll('[data-chart]');
  for (var i = 0; i < boxes.length; i++) draw(boxes[i], data[boxes[i].getAttribute('data-chart')]);
  var b = document.getElementById('print');
  if (b) b.addEventListener('click', function () { window.print(); });
  document.documentElement.setAttribute('data-ready', '1');
})();
