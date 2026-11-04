/* 极简 SVG 图表：柱状 / 折线 / 环形。无外部依赖，离线可用。 */
(function () {
  "use strict";
  var NS = "http://www.w3.org/2000/svg";
  var PALETTE = ["#0f766e", "#3b82f6", "#f59e0b", "#8b5cf6", "#ef4444", "#14b8a6", "#94a3b8"];

  function el(name, attrs) {
    var e = document.createElementNS(NS, name);
    if (attrs) {
      for (var k in attrs) {
        if (Object.prototype.hasOwnProperty.call(attrs, k)) e.setAttribute(k, attrs[k]);
      }
    }
    return e;
  }

  function text(x, y, str, attrs) {
    var t = el("text", attrs || {});
    t.setAttribute("x", x);
    t.setAttribute("y", y);
    t.textContent = str;
    return t;
  }

  function money(v) {
    return "¥" + Number(v).toLocaleString("zh-CN", {
      minimumFractionDigits: 2,
      maximumFractionDigits: 2
    });
  }

  function shortNum(v) {
    if (Math.abs(v) >= 10000) {
      var w = v / 10000;
      return (Math.round(w * 10) / 10).toString() + "万";
    }
    return Math.round(v).toLocaleString("zh-CN");
  }

  function niceMax(v) {
    if (v <= 0) return 1;
    var exp = Math.pow(10, Math.floor(Math.log(v) / Math.LN10));
    var f = v / exp;
    var nf = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
    return nf * exp;
  }

  function chartHeight(host) {
    var h = 232;
    if (host.classList.contains("chart-lg")) h = 300;
    return h;
  }

  function clear(host) {
    while (host.firstChild) host.removeChild(host.firstChild);
  }

  /* ---------------- 折线 ---------------- */
  function renderLine(host, spec, width) {
    var labels = spec.labels || [];
    var series = spec.series || [];
    if (!labels.length || !series.length) return false;

    var h = chartHeight(host);
    var w = Math.max(260, width);
    var padL = 44, padR = 16, padT = 18, padB = 30;
    var plotW = w - padL - padR, plotH = h - padT - padB;

    var max = 0;
    series.forEach(function (s) {
      (s.values || []).forEach(function (v) { if (v > max) max = v; });
    });
    max = niceMax(max);

    var svg = el("svg", { viewBox: "0 0 " + w + " " + h, role: "img" });
    var i, y, v;

    for (i = 0; i <= 4; i++) {
      y = padT + (plotH * i) / 4;
      svg.appendChild(el("line", { x1: padL, x2: w - padR, y1: y, y2: y, class: "grid-line" }));
      svg.appendChild(text(padL - 8, y + 4, shortNum((max * (4 - i)) / 4), { "text-anchor": "end" }));
    }

    var n = labels.length;
    var narrow = w < 560;
    labels.forEach(function (lab, idx) {
      var x = padL + (n === 1 ? plotW / 2 : (plotW * idx) / (n - 1));
      var anchor = idx === 0 ? "start" : idx === n - 1 ? "end" : "middle";
      var label = narrow ? lab.replace(/^\d{4}年/, "") : lab;
      svg.appendChild(text(x, h - 8, label, { "text-anchor": anchor }));
    });

    series.forEach(function (s) {
      var pts = [];
      (s.values || []).forEach(function (val, idx) {
        var x = padL + (n === 1 ? plotW / 2 : (plotW * idx) / (n - 1));
        var yy = padT + plotH - (Math.max(0, val) / max) * plotH;
        pts.push([x, yy]);
      });
      var color = s.color || PALETTE[0];
      var d = pts.map(function (p, idx) { return (idx ? "L" : "M") + p[0].toFixed(1) + "," + p[1].toFixed(1); }).join(" ");
      if (pts.length > 1) {
        var area = d + " L" + pts[pts.length - 1][0].toFixed(1) + "," + (padT + plotH) +
          " L" + pts[0][0].toFixed(1) + "," + (padT + plotH) + " Z";
        svg.appendChild(el("path", { d: area, fill: color, "fill-opacity": "0.07", stroke: "none" }));
      }
      svg.appendChild(el("path", {
        d: d, fill: "none", stroke: color, "stroke-width": "2.2",
        "stroke-linejoin": "round", "stroke-linecap": "round"
      }));
      pts.forEach(function (p, idx) {
        var dot = el("circle", { cx: p[0].toFixed(1), cy: p[1].toFixed(1), r: "3.4", class: "dot", stroke: color });
        var ttl = el("title");
        ttl.textContent = labels[idx] + " · " + s.name + " " + money((s.values || [])[idx]);
        dot.appendChild(ttl);
        svg.appendChild(dot);
      });
    });

    host.appendChild(svg);

    var legend = document.createElement("div");
    legend.className = "chart-legend";
    series.forEach(function (s) {
      var span = document.createElement("span");
      var sw = document.createElement("i");
      sw.style.background = s.color || PALETTE[0];
      span.appendChild(sw);
      span.appendChild(document.createTextNode(s.name));
      legend.appendChild(span);
    });
    host.appendChild(legend);
    return true;
  }

  /* ---------------- 环形 ---------------- */
  function renderDonut(host, spec, width) {
    var data = (spec.data || []).filter(function (d) { return Number(d.value) > 0; });
    if (!data.length) return false;
    var total = data.reduce(function (a, b) { return a + Number(b.value); }, 0);

    var wrap = document.createElement("div");
    wrap.className = "donut-wrap";

    var size = Math.min(200, Math.max(148, width * 0.44));
    var r = size / 2 - 15;
    var stroke = 22;
    var c = size / 2;
    var circ = 2 * Math.PI * r;

    var svg = el("svg", { viewBox: "0 0 " + size + " " + size, width: size, height: size, role: "img" });
    var bg = el("circle", { cx: c, cy: c, r: r, fill: "none", stroke: "#c9d3cf", "stroke-width": stroke, opacity: "0.35" });
    svg.appendChild(bg);

    var acc = 0;
    data.forEach(function (d, idx) {
      var frac = Number(d.value) / total;
      var len = frac * circ;
      var color = PALETTE[idx % PALETTE.length];
      var seg = el("circle", {
        cx: c, cy: c, r: r, fill: "none", stroke: color, "stroke-width": stroke,
        "stroke-dasharray": Math.max(0, len - 2) + " " + circ,
        "stroke-dashoffset": -acc,
        transform: "rotate(-90 " + c + " " + c + ")"
      });
      var ttl = el("title");
      ttl.textContent = d.label + " " + money(d.value) + "（" + Math.round(frac * 100) + "%）";
      seg.appendChild(ttl);
      svg.appendChild(seg);
      acc += len;
    });

    var centerTotal = Math.abs(total) >= 10000 ? "¥" + shortNum(total) : money(total);
    svg.appendChild(text(c, c - 2, centerTotal, { "text-anchor": "middle", class: "donut-total" }));
    svg.appendChild(text(c, c + 19, spec.center || "合计", { "text-anchor": "middle", class: "donut-center" }));

    var legend = document.createElement("div");
    legend.className = "donut-legend";
    data.forEach(function (d, idx) {
      var row = document.createElement("div");
      row.className = "donut-item";
      var name = document.createElement("span");
      name.className = "name";
      var sw = document.createElement("i");
      sw.style.background = PALETTE[idx % PALETTE.length];
      name.appendChild(sw);
      var nm = document.createElement("span");
      nm.textContent = d.label;
      name.appendChild(nm);
      var val = document.createElement("b");
      val.textContent = money(d.value);
      row.appendChild(name);
      row.appendChild(val);
      legend.appendChild(row);
    });

    wrap.appendChild(svg);
    wrap.appendChild(legend);
    host.appendChild(wrap);
    return true;
  }

  /* ---------------- 柱状 ---------------- */
  function renderBars(host, spec, width) {
    var data = spec.data || [];
    if (!data.length) return false;
    var h = chartHeight(host);
    var w = Math.max(260, width);
    var padL = 44, padR = 12, padT = 16, padB = 28;
    var plotW = w - padL - padR, plotH = h - padT - padB;
    var max = niceMax(Math.max.apply(null, data.map(function (d) { return Number(d.value); })));

    var svg = el("svg", { viewBox: "0 0 " + w + " " + h, role: "img" });
    for (var i = 0; i <= 4; i++) {
      var y = padT + (plotH * i) / 4;
      svg.appendChild(el("line", { x1: padL, x2: w - padR, y1: y, y2: y, class: "grid-line" }));
      svg.appendChild(text(padL - 8, y + 4, shortNum((max * (4 - i)) / 4), { "text-anchor": "end" }));
    }
    var band = plotW / data.length;
    var bw = Math.min(38, band * 0.56);
    data.forEach(function (d, idx) {
      var val = Number(d.value);
      var bh = Math.max(2, (val / max) * plotH);
      var x = padL + band * idx + (band - bw) / 2;
      var rect = el("rect", {
        x: x.toFixed(1), y: (padT + plotH - bh).toFixed(1), width: bw.toFixed(1),
        height: bh.toFixed(1), rx: Math.min(6, bw / 2), fill: spec.color || PALETTE[0], opacity: "0.85"
      });
      var ttl = el("title");
      ttl.textContent = d.label + " " + money(val);
      rect.appendChild(ttl);
      svg.appendChild(rect);
      svg.appendChild(text(x + bw / 2, h - 8, d.label, { "text-anchor": "middle" }));
    });
    host.appendChild(svg);
    return true;
  }

  function render(host) {
    var raw = host.getAttribute("data-chart");
    if (!raw) return;
    var spec;
    try { spec = JSON.parse(raw); } catch (e) { return; }
    clear(host);
    var width = host.clientWidth || 320;
    var ok = false;
    if (spec.type === "line") ok = renderLine(host, spec, width);
    else if (spec.type === "donut") ok = renderDonut(host, spec, width);
    else if (spec.type === "bars") ok = renderBars(host, spec, width);
    if (!ok) {
      var div = document.createElement("div");
      div.className = "empty";
      div.textContent = "暂无数据";
      host.appendChild(div);
    }
  }

  function renderAll() {
    var hosts = document.querySelectorAll("[data-chart]");
    Array.prototype.forEach.call(hosts, render);
  }

  var lastWidth = 0;
  window.addEventListener("resize", function () {
    var w = window.innerWidth;
    if (Math.abs(w - lastWidth) < 24) return;
    lastWidth = w;
    renderAll();
  });

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", renderAll);
  } else {
    renderAll();
  }
  window.addEventListener("load", renderAll);
})();
