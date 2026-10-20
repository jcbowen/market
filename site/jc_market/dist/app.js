/**
 * 玖祺社区商超官网 · 交互脚本
 * 覆盖：导航状态、移动端菜单、锚点高亮、滚动渐显、预览切换、
 *       价格月付/年付切换、FAQ 手风琴、演示预约弹窗与表单校验。
 */
(function () {
  'use strict';

  var $ = function (sel, root) { return (root || document).querySelector(sel); };
  var $$ = function (sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); };

  /* ---------- 顶部导航：滚动阴影 + 移动端菜单 ---------- */
  var header = $('#siteHeader');
  var burger = $('#navBurger');
  var mainNav = $('#mainNav');

  /** 根据滚动位置切换 header 的投影样式 */
  function onScrollHeader() {
    header.classList.toggle('is-scrolled', window.scrollY > 8);
  }
  window.addEventListener('scroll', onScrollHeader, { passive: true });
  onScrollHeader();

  /** 打开/关闭移动端导航菜单，同步 aria-expanded */
  function toggleNav(open) {
    var willOpen = typeof open === 'boolean' ? open : !mainNav.classList.contains('is-open');
    mainNav.classList.toggle('is-open', willOpen);
    burger.setAttribute('aria-expanded', String(willOpen));
  }
  burger.addEventListener('click', function () { toggleNav(); });
  $$('.nav-link').forEach(function (link) {
    link.addEventListener('click', function () { toggleNav(false); });
  });

  /* ---------- 锚点滚动高亮（scrollspy） ---------- */
  var sections = ['preview', 'features', 'pricing', 'testimonials', 'faq']
    .map(function (id) { return document.getElementById(id); })
    .filter(Boolean);

  /** 滚动时找出当前可视区对应的章节，高亮其导航链接 */
  function spyNav() {
    var pos = window.scrollY + 140;
    var current = '';
    sections.forEach(function (sec) {
      if (sec.offsetTop <= pos) current = sec.id;
    });
    $$('.nav-link').forEach(function (link) {
      link.classList.toggle('is-active', link.getAttribute('href') === '#' + current);
    });
  }
  window.addEventListener('scroll', spyNav, { passive: true });
  spyNav();

  /* ---------- 滚动渐显 ---------- */
  var revealEls = $$('.reveal');
  if ('IntersectionObserver' in window) {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) { e.target.classList.add('is-visible'); io.unobserve(e.target); }
      });
    }, { threshold: 0.12 });
    revealEls.forEach(function (el) { io.observe(el); });
  } else {
    revealEls.forEach(function (el) { el.classList.add('is-visible'); });
  }

  /* ---------- 产品预览 Tab 切换 ---------- */
  var tabs = $$('[data-preview-tab]');
  /** 激活指定样机：切换 tab 选中态并显示对应面板 */
  function activateTab(name) {
    tabs.forEach(function (t) {
      var on = t.dataset.previewTab === name;
      t.classList.toggle('is-active', on);
      t.setAttribute('aria-selected', String(on));
    });
    ['dash', 'pos', 'app'].forEach(function (key) {
      var pane = document.getElementById('pane-' + key);
      pane.hidden = key !== name;
      if (key === name) {
        pane.style.animation = 'none';
        void pane.offsetWidth;
        pane.style.animation = '';
      }
    });
  }
  tabs.forEach(function (t) {
    t.addEventListener('click', function () { activateTab(t.dataset.previewTab); });
  });

  /* ---------- 价格：月付 / 年付切换 ---------- */
  var billingBtns = $$('[data-billing]');
  var priceNums = $$('.price-tag b[data-monthly]');
  var yearlyNotes = $$('[data-yearly-note]');
  var perEls = $$('[data-per]');

  /** 应用付费周期：更新价格数字、单位与年付说明文案 */
  function applyBilling(mode) {
    billingBtns.forEach(function (b) {
      var on = b.dataset.billing === mode;
      b.classList.toggle('is-active', on);
      b.setAttribute('aria-pressed', String(on));
    });
    priceNums.forEach(function (el) {
      el.classList.add('is-swapping');
      setTimeout(function () {
        var v = mode === 'yearly' ? el.dataset.yearly : el.dataset.monthly;
        el.textContent = Number(v).toLocaleString('zh-CN');
        el.classList.remove('is-swapping');
      }, 140);
    });
    perEls.forEach(function (el) {
      if (!el.dataset.orig) el.dataset.orig = el.textContent.trim();
      var orig = el.dataset.orig;
      el.textContent = mode === 'yearly' ? orig.replace('/ 月', '/ 年') : orig;
    });
    yearlyNotes.forEach(function (el) { el.hidden = mode !== 'yearly'; });
  }
  billingBtns.forEach(function (b) {
    b.addEventListener('click', function () { applyBilling(b.dataset.billing); });
  });

  /* ---------- FAQ 手风琴 ---------- */
  $$('.faq-item').forEach(function (item) {
    var q = $('.faq-q', item);
    var a = $('.faq-a', item);
    q.addEventListener('click', function () {
      var open = item.classList.toggle('is-open');
      q.setAttribute('aria-expanded', String(open));
      a.hidden = !open;
    });
  });

  /* ---------- 演示预约弹窗 ---------- */
  var modal = $('#demoModal');
  var form = $('#demoForm');
  var result = $('#demoResult');
  var planLine = $('#demoPlan');
  var submitBtn = $('#demoSubmit');
  var lastFocus = null;

  /** 打开预约弹窗，可传入意向版本名显示在表单顶部 */
  function openModal(plan) {
    lastFocus = document.activeElement;
    modal.hidden = false;
    document.body.style.overflow = 'hidden';
    if (plan) {
      planLine.textContent = '意向版本：' + plan;
      planLine.hidden = false;
    } else {
      planLine.hidden = true;
    }
    resetForm();
    setTimeout(function () { $('#fName').focus(); }, 60);
  }

  /** 关闭弹窗并恢复页面滚动与焦点 */
  function closeModal() {
    modal.hidden = true;
    document.body.style.overflow = '';
    if (lastFocus && lastFocus.focus) lastFocus.focus();
  }

  /** 将弹窗重置回表单视图并清空错误提示 */
  function resetForm() {
    form.hidden = false;
    result.hidden = true;
    form.reset();
    $$('.field', form).forEach(function (f) { f.classList.remove('has-error'); });
    $$('.field-err', form).forEach(function (e) { e.hidden = true; });
    submitBtn.disabled = false;
    submitBtn.textContent = '提交预约';
  }

  $$('[data-open-demo]').forEach(function (btn) {
    btn.addEventListener('click', function () { openModal(btn.dataset.plan); });
  });
  $$('[data-close-demo]').forEach(function (el) {
    el.addEventListener('click', closeModal);
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && !modal.hidden) closeModal();
  });

  /* ---------- 表单校验与提交（前端模拟） ---------- */
  var rules = {
    fName: function (v) {
      if (!v) return '请填写姓名';
      if (v.length < 2) return '姓名至少 2 个字符';
      return '';
    },
    fPhone: function (v) {
      if (!v) return '请填写手机号';
      if (!/^1[3-9]\d{9}$/.test(v)) return '请输入 11 位有效手机号';
      return '';
    },
    fStore: function (v) {
      if (!v) return '请填写门店 / 社区名称';
      if (v.length < 2) return '名称至少 2 个字符';
      return '';
    }
  };

  /** 校验单个字段：写入错误文案并标记红框，返回是否通过 */
  function validateField(id) {
    var input = document.getElementById(id);
    var err = $('[data-err-for="' + id + '"]');
    var msg = rules[id](input.value.trim());
    err.textContent = msg;
    err.hidden = !msg;
    input.closest('.field').classList.toggle('has-error', !!msg);
    return !msg;
  }

  Object.keys(rules).forEach(function (id) {
    var input = document.getElementById(id);
    input.addEventListener('blur', function () { validateField(id); });
    input.addEventListener('input', function () {
      if (input.closest('.field').classList.contains('has-error')) validateField(id);
    });
  });

  /** 生成受理编号，如 DEMO-20261020-4837 */
  function ticketNo() {
    var d = new Date();
    var ymd = d.getFullYear() +
      String(d.getMonth() + 1).padStart(2, '0') +
      String(d.getDate()).padStart(2, '0');
    return 'DEMO-' + ymd + '-' + String(Math.floor(1000 + Math.random() * 9000));
  }

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var ok = Object.keys(rules).map(validateField).every(Boolean);
    if (!ok) {
      var firstErr = $('.field.has-error input');
      if (firstErr) firstErr.focus();
      return;
    }
    submitBtn.disabled = true;
    submitBtn.textContent = '提交中…';
    setTimeout(function () {
      $('#rName').textContent = $('#fName').value.trim();
      $('#rPhone').textContent = $('#fPhone').value.trim().replace(/(\d{3})\d{4}(\d{4})/, '$1****$2');
      $('#rStore').textContent = $('#fStore').value.trim();
      $('#rCount').textContent = $('#fCount').value;
      var note = $('#fNote').value.trim();
      $('#rNote').textContent = note;
      $('#rNoteRow').hidden = !note;
      $('#rTicket').textContent = ticketNo();
      form.hidden = true;
      result.hidden = false;
      $('.modal-card').scrollTop = 0;
    }, 900);
  });

  /* ---------- 页脚年份 ---------- */
  $('#year').textContent = String(new Date().getFullYear());
})();
