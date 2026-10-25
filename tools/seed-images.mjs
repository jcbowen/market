// seed-images.mjs —— 依据 deploy/seed-assets/images.json 绘制 dev 种子商品图（PNG）
// 风格：品类色包装剪影 + 正面标签 + 品牌与品名，全部本地矢量绘制后光栅化，不含任何外部素材，无版权风险
// 参数：argv[2] 可选，输出目录，默认 deploy/seed-assets/products/
// 返回值：无（每张图写盘后在 stdout 输出数量）
// 异常：图片清单缺失或 Chromium 未安装时抛错
// 使用示例：node tools/seed-images.mjs            # 生成/更新全部种子图
//          node tools/seed-images.mjs /tmp/png   # 只导出到临时目录做样式试看
// 依赖：tests/ 下的 @playwright/test 与已下载的 Chromium（Playwright 首次需 npx playwright install chromium）
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const ROOT_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..'); // 脚本在 tools/ 下，仓库根取上一级
// @playwright/test 是 CJS 包，ESM 下具名导出识别不到，统一从 default 上取
const playwright = (await import(
  pathToFileURL(path.join(ROOT_DIR, 'tests', 'node_modules', '@playwright', 'test', 'index.js')).href
)).default;
const { chromium } = playwright;

const MANIFEST = JSON.parse(fs.readFileSync(path.join(ROOT_DIR, 'deploy', 'seed-assets', 'images.json'), 'utf8'));
const OUT_DIR = process.argv[2] || path.join(ROOT_DIR, 'deploy', 'seed-assets', 'products');
const SIZE = 600;
fs.mkdirSync(OUT_DIR, { recursive: true });

// ---------- 颜色工具 ----------
const hex2rgb = (h) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16));
const rgb2hex = (c) => '#' + c.map((v) => Math.max(0, Math.min(255, Math.round(v))).toString(16).padStart(2, '0')).join('');
/** 与白色按比例混合，得到包装的高光色 */
const tint = (h, r) => rgb2hex(hex2rgb(h).map((v) => v + (255 - v) * r));
/** 与黑色按比例混合，得到封口、瓶盖等深色部件 */
const shade = (h, r) => rgb2hex(hex2rgb(h).map((v) => v * (1 - r)));
const INK = '#3F3A34';
const SUB = '#8A8378';
const GREY = '#C9C4BC';

// ---------- 包装剪影：每种 shape 返回 SVG 片段与可选的标签矩形 ----------
// 统一画布 600x600，剪影主体大致占据 y=110~470 区域
function silhouette(shape, c) {
  const light = tint(c, 0.45);
  const pale = tint(c, 0.72);
  const dark = shade(c, 0.28);
  const seal = shade(c, 0.42);
  const white = '#FFFFFF';
  switch (shape) {
    case 'bag': // 薯片/辣条类枕形袋：主体 + 上下锯齿封口
      return `
        <rect x="176" y="150" width="248" height="290" rx="20" fill="${c}"/>
        <rect x="176" y="150" width="70" height="290" rx="20" fill="${light}" opacity="0.55"/>
        <path d="M176 132 H424 v10 l-20 12 -20-12 -20 12 -20-12 -20 12 -20-12 -20 12 -20-12 -20 12 -20-12 -20 12 -20-12 z" fill="${seal}"/>
        <path d="M176 458 H424 v-10 l-20-12 -20 12 -20-12 -20 12 -20-12 -20 12 -20-12 -20 12 -20-12 -20 12 -20-12 z" fill="${seal}"/>
        <rect x="212" y="248" width="176" height="112" rx="12" fill="${white}" opacity="0.85"/>`;
    case 'pouch': // 自立袋：斜切左上角 + 拉链线
      return `
        <path d="M200 168 q0-24 24-24 h152 q24 0 24 24 v272 q0 24-24 24 h-152 q-24 0-24-24 z" fill="${c}"/>
        <path d="M200 168 q0-24 24-24 h60 v320 h-60 q-24 0-24-24 z" fill="${light}" opacity="0.5"/>
        <rect x="204" y="196" width="192" height="12" rx="6" fill="${white}" opacity="0.75"/>
        <rect x="286" y="132" width="32" height="20" rx="4" fill="${seal}"/>
        <rect x="222" y="248" width="156" height="112" rx="12" fill="${white}" opacity="0.85"/>`;
    case 'pack': // 饼干/面包类枕包：横向主体 + 两端锯齿
      return `
        <rect x="164" y="206" width="272" height="188" rx="18" fill="${c}"/>
        <rect x="164" y="206" width="272" height="46" rx="18" fill="${light}" opacity="0.5"/>
        <path d="M164 216 l-34-14 v196 l34-14 z" fill="${dark}"/>
        <path d="M436 216 l34-14 v196 l-34-14 z" fill="${dark}"/>`;
    case 'box': // 纸盒（牛奶/水饺/咖啡）：正面 + 顶面 + 侧面的立体感
      return `
        <path d="M186 186 h228 v274 h-228 z" fill="${c}"/>
        <path d="M186 186 l40-44 h228 l-40 44 z" fill="${light}"/>
        <path d="M414 186 l40-44 v274 l-40 44 z" fill="${dark}"/>
        <rect x="206" y="212" width="120" height="10" rx="5" fill="${white}" opacity="0.6"/>`;
    case 'carton': // 利乐砖：屋形顶
      return `
        <path d="M224 210 h152 v250 h-152 z" fill="${c}"/>
        <path d="M224 210 l28-58 h96 l28 58 z" fill="${light}"/>
        <rect x="238" y="140" width="124" height="18" rx="6" fill="${seal}"/>
        <rect x="244" y="240" width="112" height="10" rx="5" fill="${white}" opacity="0.6"/>`;
    case 'bottle': // PET 饮料瓶：瓶盖 + 瓶颈 + 瓶身 + 防滑环
      return `
        <rect x="276" y="104" width="48" height="34" rx="7" fill="${seal}"/>
        <rect x="282" y="138" width="36" height="26" fill="${dark}"/>
        <path d="M282 164 h36 l32 42 v218 q0 22-22 22 h-56 q-22 0-22-22 v-218 z" fill="${c}"/>
        <path d="M282 164 h16 l-16 42 v218 q0 22 6 22 h-10 q-22 0-22-22 v-218 z" fill="${light}" opacity="0.55"/>
        <rect x="240" y="264" width="120" height="104" rx="14" fill="${white}" opacity="0.82"/>
        <rect x="240" y="392" width="120" height="9" rx="4" fill="${dark}" opacity="0.5"/>
        <rect x="240" y="410" width="120" height="9" rx="4" fill="${dark}" opacity="0.5"/>`;
    case 'can': // 易拉罐：顶盖椭圆 + 罐身
      return `
        <rect x="212" y="150" width="176" height="300" fill="${c}"/>
        <ellipse cx="300" cy="450" rx="88" ry="22" fill="${dark}"/>
        <ellipse cx="300" cy="150" rx="88" ry="26" fill="#D9DDE2"/>
        <ellipse cx="300" cy="150" rx="64" ry="17" fill="#C2C7CE"/>
        <circle cx="318" cy="146" r="9" fill="#9AA1AA"/>
        <rect x="212" y="252" width="176" height="112" fill="${white}" opacity="0.82"/>
        <rect x="228" y="150" width="26" height="300" fill="${light}" opacity="0.5"/>`;
    case 'cup': // 桶装面：上宽下窄桶身 + 盖沿
      return `
        <path d="M208 200 h184 l-20 250 h-144 z" fill="${c}"/>
        <path d="M208 200 h44 l-10 250 h-30 z" fill="${light}" opacity="0.5"/>
        <path d="M190 166 h220 l-18 40 h-184 z" fill="${dark}"/>
        <ellipse cx="300" cy="166" rx="110" ry="20" fill="${pale}"/>
        <rect x="222" y="266" width="156" height="96" rx="10" fill="${white}" opacity="0.8"/>`;
    case 'jar': // 圆罐 / 罐头：大盖 + 罐身
      return `
        <rect x="222" y="138" width="156" height="44" rx="10" fill="${seal}"/>
        <rect x="232" y="182" width="136" height="12" fill="${dark}"/>
        <path d="M226 194 h148 v234 q0 20-20 20 h-108 q-20 0-20-20 z" fill="${c}"/>
        <rect x="240" y="240" width="120" height="120" rx="12" fill="${white}" opacity="0.82"/>
        <rect x="244" y="198" width="24" height="250" fill="${light}" opacity="0.45"/>`;
    case 'jug': // 油桶 / 洗衣液：提手 + 瓶盖
      return `
        <rect x="262" y="126" width="72" height="40" rx="8" fill="${seal}"/>
        <rect x="272" y="164" width="52" height="26" fill="${dark}"/>
        <path d="M204 190 h196 q16 0 16 16 v246 q0 18-18 18 h-192 q-18 0-18-18 v-246 q0-16 16-16 z" fill="${c}"/>
        <path d="M400 236 h30 q26 0 26 30 v52 q0 30-26 30 h-30 v-24 h26 q8 0 8-10 v-44 q0-10-8-10 h-26 z" fill="${dark}"/>
        <rect x="226" y="270" width="152" height="128" rx="14" fill="${white}" opacity="0.85"/>`;
    case 'ricebag': // 米袋：鼓身 + 顶部折叠
      return `
        <path d="M196 224 q-14 122 4 214 q6 24 30 24 h140 q24 0 30-24 q18-92 4-214 z" fill="${c}"/>
        <path d="M196 224 q-14 122 4 214 q6 24 30 24 h30 q-24-160-8-238 z" fill="${light}" opacity="0.5"/>
        <rect x="208" y="166" width="184" height="58" rx="10" fill="${seal}"/>
        <rect x="232" y="292" width="136" height="118" rx="12" fill="${white}" opacity="0.85"/>`;
    case 'tissue': // 软包抽纸：开口椭圆 + 抽出的纸
      return `
        <rect x="168" y="222" width="264" height="196" rx="26" fill="${c}"/>
        <rect x="168" y="222" width="264" height="52" rx="26" fill="${light}" opacity="0.5"/>
        <ellipse cx="300" cy="238" rx="62" ry="18" fill="${dark}"/>
        <path d="M262 236 q20-64 46-58 q30 8 34 58 z" fill="#FDFDFB"/>
        <rect x="196" y="304" width="208" height="86" rx="12" fill="${white}" opacity="0.8"/>`;
    case 'tube': // 牙膏：尾部压扁 + 管身 + 盖
      return `
        <rect x="248" y="140" width="104" height="26" rx="6" fill="${seal}"/>
        <path d="M252 166 h96 v228 q0 34-48 34 q-48 0-48-34 z" fill="${c}"/>
        <path d="M252 166 h30 v262 q-30-8-30-34 z" fill="${light}" opacity="0.5"/>
        <rect x="270" y="428" width="60" height="46" rx="8" fill="${dark}"/>
        <rect x="262" y="220" width="76" height="120" rx="10" fill="${white}" opacity="0.8"/>`;
    case 'bar': // 排块巧克力 / 能量棒：外膜 + 分格
      return `
        <rect x="170" y="200" width="260" height="200" rx="18" fill="${c}"/>
        <rect x="196" y="222" width="208" height="156" rx="10" fill="${dark}"/>
        <g fill="${shade(c, 0.55)}">
          ${[0, 1, 2, 3].map((i) => `<rect x="${206 + i * 51}" y="232" width="42" height="62" rx="6"/>`).join('')}
          ${[0, 1, 2, 3].map((i) => `<rect x="${206 + i * 51}" y="306" width="42" height="62" rx="6"/>`).join('')}
        </g>
        <rect x="170" y="200" width="52" height="200" rx="18" fill="${light}" opacity="0.4"/>`;
    case 'stick': // 火腿肠 / 雪糕：长条主体 + 两端金属卡扣
      return `
        <rect x="252" y="118" width="96" height="332" rx="46" fill="${c}"/>
        <rect x="266" y="118" width="26" height="332" rx="13" fill="${light}" opacity="0.5"/>
        <rect x="256" y="150" width="88" height="14" rx="7" fill="${seal}"/>
        <rect x="256" y="404" width="88" height="14" rx="7" fill="${seal}"/>
        <rect x="266" y="212" width="68" height="132" rx="10" fill="${white}" opacity="0.82"/>
        <path d="M292 450 h16 v68 q0 10-8 10 q-8 0-8-10 z" fill="#D9BE93"/>`;
    case 'eggcarton': // 蛋托：盒体 + 蛋列
      return `
        <path d="M172 300 h256 v150 h-256 z" fill="${c}"/>
        <path d="M172 300 l34-58 h256 l-34 58 z" fill="${light}"/>
        <path d="M428 300 l34-58 v150 l-34 58 z" fill="${dark}"/>
        ${[0, 1, 2, 3].map((i) => `<ellipse cx="${212 + i * 60}" cy="308" rx="26" ry="18" fill="${seal}" opacity="0.45"/>`).join('')}
        ${[0, 1, 2].map((i) => `<ellipse cx="${242 + i * 60}" cy="252" rx="24" ry="21" fill="#F4E3C8"/>`).join('')}
        ${[0, 1, 2].map((i) => `<ellipse cx="${242 + i * 60}" cy="246" rx="24" ry="20" fill="#FBF0DC"/>`).join('')}`;
    default:
      return `<rect x="200" y="200" width="200" height="200" rx="20" fill="${c}"/>`;
  }
}

// 各 shape 的正面标签区 [x, y, w, h]；bar / eggcarton 本身即视觉主体，不加标签
const LABELS = {
  bag: [212, 248, 176, 112],
  pouch: [222, 248, 156, 112],
  pack: [214, 246, 172, 108],
  box: [214, 240, 172, 150],
  carton: [244, 236, 112, 150],
  bottle: [240, 264, 120, 104],
  can: [212, 252, 176, 112],
  cup: [222, 266, 156, 96],
  jar: [240, 240, 120, 120],
  jug: [226, 270, 152, 128],
  ricebag: [232, 292, 136, 118],
  tissue: [196, 304, 208, 86],
  tube: [262, 220, 76, 120],
  stick: [266, 212, 68, 132],
};

/** 标签区：白底 + 品牌色条 + 占位文字行，模拟真实包装的正面印刷 */
function label(item, c) {
  const r = LABELS[item.shape];
  if (!r) return '';
  const [x, y, w, h] = r;
  const pad = 14;
  return `
    <rect x="${x}" y="${y}" width="${w}" height="${h}" rx="12" fill="#FFFFFF" opacity="0.94"/>
    <rect x="${x + pad}" y="${y + pad}" width="${Math.round(w * 0.46)}" height="12" rx="6" fill="${c}"/>
    <rect x="${x + pad}" y="${y + pad + 26}" width="${w - pad * 2}" height="9" rx="4" fill="${GREY}"/>
    <rect x="${x + pad}" y="${y + pad + 44}" width="${Math.round((w - pad * 2) * 0.62)}" height="9" rx="4" fill="${GREY}"/>
    <rect x="${x + pad}" y="${y + h - 24}" width="${Math.round(w * 0.34)}" height="9" rx="4" fill="${tint(c, 0.35)}"/>`;
}

// ---------- 卡片：上方品牌、中间剪影、下方品名 ----------
function card(item) {
  const c = item.color;
  const shape = item.shape;
  const isAlt = item.note === 'alt';
  const body = silhouette(shape, c) + label(item, c);
  const zoom = isAlt ? 'transform="rotate(-8 300 300) translate(300 300) scale(1.3) translate(-300 -300)"' : '';
  const name = isAlt ? `${item.label} 细节` : item.label;
  const nameSize = name.length > 7 ? 40 : name.length > 5 ? 46 : 52;
  const maxTextWidth = 520;
  const est = name.length * nameSize;
  const textLen = est > maxTextWidth ? ` textLength="${maxTextWidth}" lengthAdjust="spacingAndGlyphs"` : '';
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${SIZE}" height="${SIZE}" viewBox="0 0 600 600">
  <g>
    <rect width="600" height="600" fill="${tint(c, 0.9)}"/>
    <circle cx="300" cy="290" r="215" fill="${tint(c, 0.78)}"/>
    <ellipse cx="300" cy="470" rx="150" ry="26" fill="${shade(c, 0.5)}" opacity="0.12"/>
    <g ${zoom}>${body}</g>
    ${isAlt ? '' : `<text x="300" y="88" font-family="PingFang SC, Hiragino Sans GB, sans-serif" font-size="26" letter-spacing="4" fill="${SUB}" text-anchor="middle">${item.brand}</text>`}
    <text x="300" y="556"${textLen} font-family="PingFang SC, Hiragino Sans GB, sans-serif" font-size="${nameSize}" font-weight="600" fill="${INK}" text-anchor="middle">${name}</text>
  </g>
</svg>`;
}

const html = `<!doctype html><meta charset="utf-8"><style>
  body{margin:0;background:#fff}
  .shot{width:600px;height:600px;display:block}
  .shot+ .shot{margin-top:4px}
</style>
${MANIFEST.map((it) => `<div class="shot" data-file="${it.file}" style="width:600px;height:600px">${card(it)}</div>`).join('')}`;

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 640, height: 640 }, deviceScaleFactor: 1 });
  await page.setContent(html, { waitUntil: 'load' });
  await page.evaluate(() => document.fonts.ready);
  for (let i = 0; i < MANIFEST.length; i += 1) {
    await page.locator('.shot').nth(i).screenshot({ path: path.join(OUT_DIR, MANIFEST[i].file) });
  }
  await browser.close();
  console.log('已绘制', MANIFEST.length, '张 →', OUT_DIR);
})();
