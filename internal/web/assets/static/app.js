/* 记账本 · 交互增强（无依赖） */
(function () {
  "use strict";

  /* PWA：仅在安全上下文（HTTPS / localhost）注册 */
  if ("serviceWorker" in navigator) {
    var secure = location.protocol === "https:" ||
      location.hostname === "localhost" || location.hostname === "127.0.0.1";
    if (secure) {
      window.addEventListener("load", function () {
        navigator.serviceWorker.register("/sw.js").catch(function () { /* 忽略 */ });
      });
    }
  }

  /* 保存成功提示自动消失 */
  var toast = document.querySelector("[data-toast]");
  if (toast) {
    window.setTimeout(function () {
      toast.style.transition = "opacity .3s";
      toast.style.opacity = "0";
      window.setTimeout(function () { toast.remove(); }, 320);
    }, 2200);
  }

  var form = document.querySelector("[data-tx-form]");
  if (form) {
    initDirection(form);
    initUpload(form);
  }

  /* 收入/支出切换时，过滤分类下拉 */
  function initDirection(form) {
    var cat = form.querySelector("[data-category]");
    if (!cat) return;
    var radios = form.querySelectorAll('input[name="direction"]');

    function sync() {
      var checked = form.querySelector('input[name="direction"]:checked');
      var dir = checked ? checked.value : "out";
      var matched = false;
      Array.prototype.forEach.call(cat.options, function (opt) {
        if (!opt.value) return;
        var ok = opt.getAttribute("data-dir") === dir;
        opt.hidden = !ok;
        opt.disabled = !ok;
        if (ok && opt.selected) matched = true;
      });
      if (!matched) cat.value = "";
    }

    Array.prototype.forEach.call(radios, function (r) { r.addEventListener("change", sync); });
    sync();
  }

  function initUpload(form) {
    var box = form.querySelector("[data-upload]");
    if (!box) return;
    var input = box.querySelector("[data-upload-input]");
    var pending = box.querySelector("[data-pending]");
    if (!input || !pending) return;

    form.addEventListener("submit", function (e) {
      if (pending.querySelector(".pending")) {
        e.preventDefault();
        window.alert("图片还在上传，等一两秒再保存。");
      }
    });

    input.addEventListener("change", function () {
      Array.prototype.forEach.call(input.files, function (f) { upload(f); });
      input.value = "";
    });

    ["dragenter", "dragover"].forEach(function (ev) {
      box.addEventListener(ev, function (e) {
        e.preventDefault();
        box.classList.add("dragover");
      });
    });
    ["dragleave", "drop"].forEach(function (ev) {
      box.addEventListener(ev, function (e) {
        e.preventDefault();
        box.classList.remove("dragover");
      });
    });
    box.addEventListener("drop", function (e) {
      var files = e.dataTransfer && e.dataTransfer.files;
      if (files) Array.prototype.forEach.call(files, function (f) { upload(f); });
    });

    function upload(file) {
      if (!/^image\//.test(file.type)) {
        window.alert("只能上传图片（JPG / PNG / WebP）");
        return;
      }
      var thumb = document.createElement("div");
      thumb.className = "thumb pending";
      var img = document.createElement("img");
      img.alt = "上传中";
      img.src = URL.createObjectURL(file);
      var note = document.createElement("div");
      note.className = "muted small";
      note.textContent = "上传中…";
      thumb.appendChild(img);
      thumb.appendChild(note);
      pending.appendChild(thumb);

      var fd = new FormData();
      fd.append("file", file);
      fetch("/upload", {
        method: "POST",
        body: fd,
        headers: { "X-Requested-With": "fetch", "Accept": "application/json" }
      }).then(function (r) {
        return r.json().then(function (j) {
          if (!r.ok) throw new Error(j.error || "上传失败");
          return j;
        });
      }).then(function (j) {
        thumb.classList.remove("pending");
        note.remove();
        var hidden = document.createElement("input");
        hidden.type = "hidden";
        hidden.name = "attach";
        hidden.value = j.key;
        thumb.appendChild(hidden);

        var del = document.createElement("button");
        del.type = "button";
        del.className = "thumb-del";
        del.textContent = "移除";
        del.addEventListener("click", function () {
          hidden.remove();
          thumb.remove();
        });
        thumb.appendChild(del);
        img.src = j.url;
      }).catch(function (err) {
        thumb.remove();
        window.alert(err.message || "上传失败，请重试");
      });
    }
  }
})();
