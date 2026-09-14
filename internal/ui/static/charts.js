/* Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
   See the LICENSE file in the repository root for the full terms. */

/* Hover for the server-rendered charts. Everything it shows is already in
 * the markup: Go wrote the values onto data-* attributes and every mark has
 * a <title>. This file moves a cursor, builds a card from those attributes,
 * and highlights one brand across the engine strips. It computes nothing
 * about the data and draws no mark of its own. With it off, nothing is
 * missing; it is only slower to read. */
(function () {
  'use strict';

  var esc = function (s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  };

  /* The race: a crosshair that snaps to the nearest measured day, and a
   * card listing every brand's value on it. Snapping to measured ticks
   * means hovering inside a shaded gap never invents a value. */
  function race(wrap) {
    var svg = wrap.querySelector('svg.chart-race');
    var tip = wrap.querySelector('.tip');
    if (!svg || !tip) return;
    var cursor = svg.querySelector('.cursor');
    var ticks = Array.prototype.slice.call(svg.querySelectorAll('.tick'));
    var dots = Array.prototype.slice.call(svg.querySelectorAll('.dot'));
    if (!ticks.length) return;
    var vb = svg.viewBox.baseVal;
    var plotTop = svg.querySelector('.grid') ? parseFloat(svg.querySelector('.grid').getAttribute('y1')) : 0;
    var current = -1;

    function show(i) {
      if (i === current) return;
      current = i;
      var tick = ticks[i];
      var x = parseFloat(tick.getAttribute('data-x'));
      cursor.setAttribute('x1', x);
      cursor.setAttribute('x2', x);
      svg.classList.add('is-hover');
      var rows = [];
      dots.forEach(function (d) {
        var on = d.getAttribute('data-i') === String(i);
        d.classList.toggle('is-on', on);
        if (on) rows.push(d);
      });
      rows.sort(function (a, b) { return parseFloat(b.getAttribute('data-v')) - parseFloat(a.getAttribute('data-v')); });
      var n = tick.getAttribute('data-n');
      var thin = tick.hasAttribute('data-thin');
      var html = '<div class="tip-head"><span class="tip-day">' + esc(tick.getAttribute('data-day')) + '</span>' +
        '<span class="tip-n' + (thin ? ' tip-thin' : '') + '">' + esc(n) + (n === '1' ? ' answer' : ' answers') + (thin ? ', thin sample' : '') + '</span></div>';
      rows.forEach(function (d) {
        var own = d.classList.contains('series-own');
        var cls = Array.prototype.filter.call(d.classList, function (c) { return /^s-/.test(c); }).join(' ');
        html += '<div class="tip-row ' + esc(cls) + (own ? ' own' : '') + '"><span class="swatch"></span>' +
          '<span class="tip-name">' + esc(d.getAttribute('data-brand')) + (own ? ' <span class="tag">you</span>' : '') + '</span>' +
          '<span class="tip-val">' + esc(d.getAttribute('data-v')) + '%</span></div>';
      });
      tip.innerHTML = html;
      tip.hidden = false;
      var box = svg.getBoundingClientRect();
      var scale = box.width / vb.width;
      var px = x * scale;
      var left = px + 16;
      if (left + tip.offsetWidth > box.width) left = px - 16 - tip.offsetWidth;
      tip.style.left = Math.max(0, left) + 'px';
      tip.style.top = (plotTop * scale) + 'px';
    }

    function hide() {
      current = -1;
      svg.classList.remove('is-hover');
      dots.forEach(function (d) { d.classList.remove('is-on'); });
      tip.hidden = true;
    }

    svg.addEventListener('pointermove', function (e) {
      var box = svg.getBoundingClientRect();
      var vx = (e.clientX - box.left) / box.width * vb.width;
      var best = 0, bestD = Infinity;
      ticks.forEach(function (t, i) {
        var d = Math.abs(parseFloat(t.getAttribute('data-x')) - vx);
        if (d < bestD) { bestD = d; best = i; }
      });
      show(best);
    });
    svg.addEventListener('pointerleave', hide);
  }

  /* The donut: hovering a slice puts its value in the centre; leaving puts
   * yours back. */
  function donut(svg) {
    var value = svg.querySelector('.donut-value');
    var label = svg.querySelector('.donut-label');
    if (!value || !label) return;
    var v0 = value.textContent, l0 = label.textContent;
    Array.prototype.forEach.call(svg.querySelectorAll('.arc'), function (arc) {
      arc.addEventListener('pointerenter', function () {
        value.textContent = arc.getAttribute('data-v') + '%';
        label.textContent = arc.getAttribute('data-name');
      });
      arc.addEventListener('pointerleave', function () {
        value.textContent = v0;
        label.textContent = l0;
      });
    });
  }

  /* The engine strips: hovering a dot lights that brand on every row, so
   * one gesture answers "where does NVIDIA stand everywhere". */
  function strips(svg) {
    var pts = Array.prototype.slice.call(svg.querySelectorAll('.pt'));
    var label = svg.querySelector('.ptlabel');
    if (!pts.length || !label) return;
    var w = svg.viewBox.baseVal.width;
    pts.forEach(function (pt) {
      pt.addEventListener('pointerenter', function () {
        var brand = pt.getAttribute('data-brand');
        svg.classList.add('has-focus');
        pts.forEach(function (p) { p.classList.toggle('is-on', p.getAttribute('data-brand') === brand); });
        var cx = parseFloat(pt.getAttribute('cx')), cy = parseFloat(pt.getAttribute('cy'));
        var flip = cx > 0.75 * w;
        label.setAttribute('x', flip ? cx - 10 : cx + 10);
        label.setAttribute('y', cy);
        label.setAttribute('text-anchor', flip ? 'end' : 'start');
        label.setAttribute('dominant-baseline', 'middle');
        label.textContent = brand + ' ' + pt.getAttribute('data-v') + '%';
      });
    });
    svg.addEventListener('pointerleave', function () {
      svg.classList.remove('has-focus');
      pts.forEach(function (p) { p.classList.remove('is-on'); });
      label.textContent = '';
    });
  }

  function init() {
    Array.prototype.forEach.call(document.querySelectorAll('.chart-wrap[data-chart="race"]'), race);
    Array.prototype.forEach.call(document.querySelectorAll('svg.chart-donut'), donut);
    Array.prototype.forEach.call(document.querySelectorAll('svg.chart-engines'), strips);
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();
