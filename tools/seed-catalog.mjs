// seed-catalog.mjs —— 生成 dev 种子数据的「商品目录」源数据（社区便利店 · 预包装食品）
// 参数：无；返回值：无（直跑输出 SQL 到 stdout 供人工核对，图片清单写入 deploy/seed-assets/images.json）
// 异常：商品缺少图片 slug（FILE_SLUG）时抛错退出，避免生成引用不存在图片的种子
// 使用示例：
//   node tools/seed-catalog.mjs                       # 输出目录 SQL（仅人工核对用，不入库）
//   node tools/seed-images.mjs                       # 依据 images.json 绘制商品图到 deploy/seed-assets/products/
//   import { categories, products, collectSeedCatalog } from './seed-catalog.mjs'  # 数据包生成器（gen-data-package.mjs）复用同一份源数据
// 约束：本文件是商品目录的唯一数据源，collectSeedCatalog() 供 gen-data-package.mjs
//       生成 full-demo.json（编译进 product-service，经数据导入/初始化链路落库）。
//       改 id 或价格后需重新生成 full-demo.json 并提交。
// 注意：种子批次（MANUAL_BATCHES）演示了与进销存单据咬合的口径（Σ批次=SKU 库存），
//       订单/进销存样本段按批次号引用这些批次；批次本身随目录段落库。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const ROOT_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..'); // 脚本在 tools/ 下，仓库根取上一级
const ASSET_DIR = path.join(ROOT_DIR, 'deploy', 'seed-assets');
fs.mkdirSync(ASSET_DIR, { recursive: true });

const IMG_DIR = '/uploads/seed/products';

// ---------- 分类 ----------
const categories = [
  { id: 1, cid: 1, name: '休闲零食', sort: 10 },
  { id: 2, cid: 1, name: '酒水饮料', sort: 20 },
  { id: 4, cid: 1, name: '方便速食', sort: 30 },
  { id: 5, cid: 1, name: '乳品烘焙', sort: 40 },
  { id: 6, cid: 1, name: '粮油调味', sort: 50 },
  { id: 7, cid: 1, name: '日用百货', sort: 60 },
  { id: 8, cid: 1, name: '冷冻食品', sort: 70 },
  { id: 9, cid: 1, name: '糖果巧克力', sort: 80 },
  { id: 10, cid: 1, name: '冲调饮品', sort: 90 },
  { id: 3, cid: 2, name: '乳品蛋品', sort: 10 },
  { id: 11, cid: 2, name: '休闲零食', sort: 20 },
  { id: 12, cid: 2, name: '酒水饮料', sort: 30 },
  { id: 13, cid: 2, name: '方便速食', sort: 40 },
  { id: 14, cid: 2, name: '粮油调味', sort: 50 },
  { id: 15, cid: 2, name: '日用百货', sort: 60 },
];

// ---------- 商品 ----------
// sku: [skuId, code, specName, price, original, cost, stock, safety, expireDays(null=不限期), prodAgo, split?]
// split=true 时拆两个批次（6:4），用于演示 FEFO
const products = [
  // 休闲零食
  {
    id: 1, cid: 1, cat: 1, name: '魔小鲜 大面筋辣条', short: '大面筋辣条', brand: '魔小鲜',
    desc: '经典川香辣条，独立小包装，追剧解馋开袋即食', unit: '袋', multi: true, shape: 'bag',
    color: '#DC2626', vSales: 320, sort: 100, status: 1, alt: true,
    spec: { id: 1, name: '规格', values: [[1, '小包106g'], [2, '大包226g']] },
    skus: [
      [1, 'SNACK-LATIAO-S', '小包106g', 3.50, 4.50, 2.00, 80, 20, 150, 35, true],
      [2, 'SNACK-LATIAO-L', '大包226g', 6.50, 8.00, 3.80, 40, 10, 150, 24],
    ],
  },
  {
    id: 3, cid: 1, cat: 1, name: '乐游 原味薯片', short: '原味薯片', brand: '乐游',
    desc: '厚切马铃薯片，咸香酥脆，70g 便携装', unit: '袋', multi: false, shape: 'bag',
    color: '#F59E0B', vSales: 260, sort: 90, status: 1,
    skus: [[4, 'SNACK-CHIPS-70', '70g', 5.50, 6.90, 3.00, 90, 30, 240, 20]],
  },
  {
    id: 5, cid: 1, cat: 1, name: '麦香源 巧克力夹心饼干', short: '夹心饼干', brand: '麦香源',
    desc: '酥脆饼干夹可可奶油心，116g 家庭小袋装', unit: '袋', multi: false, shape: 'pack',
    color: '#8B5CF6', vSales: 140, sort: 80, status: 1,
    skus: [[6, 'SNACK-COOKIE-116', '116g', 6.90, 8.50, 4.10, 96, 20, 210, 18]],
  },
  {
    id: 6, cid: 1, cat: 1, name: '果仁季 每日坚果', short: '每日坚果', brand: '果仁季',
    desc: '七种果仁独立小包，25g×7 混合装，办公室常备', unit: '盒', multi: false, shape: 'box',
    color: '#B45309', vSales: 88, sort: 70, status: 1,
    skus: [[7, 'SNACK-NUTS-7', '25g×7袋', 29.90, 39.90, 19.50, 42, 10, 180, 25]],
  },
  {
    id: 7, cid: 1, cat: 1, name: '肉匠 蜜汁猪肉脯', short: '蜜汁猪肉脯', brand: '肉匠',
    desc: '厚切猪肉脯，蜜汁入味，100g 即食装', unit: '袋', multi: false, shape: 'pouch',
    color: '#BE123C', vSales: 66, sort: 60, status: 1,
    skus: [[8, 'SNACK-JERKY-100', '100g', 19.90, 25.90, 12.80, 36, 8, 150, 30]],
  },
  {
    id: 8, cid: 1, cat: 1, name: '雪旺 咸味雪饼', short: '咸味雪饼', brand: '雪旺',
    desc: '米饼夹心，松脆咸香，84g 经典怀旧味', unit: '袋', multi: false, shape: 'pack',
    color: '#FBBF24', vSales: 120, sort: 50, status: 1,
    skus: [[9, 'SNACK-SNOWCAKE-84', '84g', 7.50, 9.00, 4.50, 68, 15, 240, 22]],
  },
  {
    id: 9, cid: 1, cat: 1, name: '魔小鲜 香辣魔芋爽', short: '香辣魔芋爽', brand: '魔小鲜',
    desc: '低负担辣味零食，Q 弹爽口，50g 独立装', unit: '袋', multi: false, shape: 'pouch',
    color: '#EA580C', vSales: 210, sort: 40, status: 1,
    skus: [[10, 'SNACK-KONJAC-50', '50g', 3.20, 4.00, 1.70, 120, 25, 270, 15]],
  },

  // 酒水饮料
  {
    id: 2, cid: 1, cat: 2, name: '碧源 饮用天然水', short: '饮用天然水', brand: '碧源',
    desc: '弱碱性天然水，550ml 随身装，整箱更划算', unit: '瓶', multi: false, shape: 'bottle',
    color: '#0EA5E9', vSales: 480, sort: 100, status: 1,
    skus: [[3, 'DRINK-WATER-550', '550ml', 2.00, 2.80, 1.20, 200, 40, 720, 40, true]],
  },
  {
    id: 10, cid: 1, cat: 2, name: '气泡山 经典可乐', short: '经典可乐', brand: '气泡山',
    desc: '冰镇更带劲的碳酸饮料，330ml 易拉罐装', unit: '罐', multi: false, shape: 'can',
    color: '#B91C1C', vSales: 350, sort: 90, status: 1,
    skus: [[11, 'DRINK-COLA-330', '330ml', 3.00, 3.50, 1.90, 144, 30, 365, 30]],
  },
  {
    id: 11, cid: 1, cat: 2, name: '气泡山 柠檬味汽水', short: '柠檬汽水', brand: '气泡山',
    desc: '清爽柠檬口味汽水，500ml 大瓶装', unit: '瓶', multi: false, shape: 'bottle',
    color: '#22C55E', vSales: 220, sort: 80, status: 1,
    skus: [[12, 'DRINK-SPRITE-500', '500ml', 3.50, 4.00, 2.10, 96, 20, 300, 26]],
  },
  {
    id: 12, cid: 1, cat: 2, name: '茶语时光 冰红茶', short: '冰红茶', brand: '茶语时光',
    desc: '柠檬风味红茶，1L 分享装，夏日解腻', unit: '瓶', multi: false, shape: 'bottle',
    color: '#C2410C', vSales: 300, sort: 70, status: 1,
    skus: [[13, 'DRINK-ICETEA-1L', '1L', 4.50, 5.50, 2.60, 84, 18, 270, 28]],
  },
  {
    id: 13, cid: 1, cat: 2, name: '茶语时光 无糖乌龙茶', short: '无糖乌龙茶', brand: '茶语时光',
    desc: '0 糖 0 卡原叶萃取，500ml 无糖茶饮', unit: '瓶', multi: false, shape: 'bottle',
    color: '#15803D', vSales: 160, sort: 60, status: 1,
    skus: [[14, 'DRINK-OOLONG-500', '500ml', 5.00, 6.00, 3.00, 72, 15, 300, 20]],
  },
  {
    id: 14, cid: 1, cat: 2, name: '倍力特 维生素功能饮料', short: '功能饮料', brand: '倍力特',
    desc: '补充能量与维生素，250ml 金罐装', unit: '罐', multi: false, shape: 'can',
    color: '#F59E0B', vSales: 190, sort: 50, status: 1,
    skus: [[15, 'DRINK-ENERGY-250', '250ml', 6.50, 7.50, 4.20, 60, 12, 365, 32]],
  },
  {
    id: 15, cid: 1, cat: 2, name: '青岛 经典啤酒', short: '经典啤酒', brand: '青岛',
    desc: '麦香清爽的拉格啤酒，500ml 罐装，冷藏更佳', unit: '罐', multi: false, shape: 'can',
    color: '#EAB308', vSales: 240, sort: 40, status: 1,
    skus: [[16, 'DRINK-BEER-500', '500ml', 6.00, 7.00, 3.80, 90, 20, 180, 24]],
  },
  {
    id: 16, cid: 1, cat: 2, name: '优乐乳 乳酸菌饮品', short: '乳酸菌饮品', brand: '优乐乳',
    desc: '发酵乳酸菌风味，250ml×6 连包，饭后一瓶', unit: '提', multi: false, shape: 'pack',
    color: '#F472B6', vSales: 130, sort: 30, status: 1,
    skus: [[17, 'DRINK-LACTIC-6', '250ml×6', 12.90, 15.90, 8.60, 48, 10, 180, 16]],
  },

  // 方便速食
  {
    id: 17, cid: 1, cat: 4, name: '面匠 红烧牛肉面', short: '红烧牛肉面', brand: '面匠',
    desc: '经典红烧汤底，桶装免洗碗，夜宵加班必备', unit: '桶', multi: false, shape: 'cup',
    color: '#DC2626', vSales: 420, sort: 100, status: 1,
    skus: [[18, 'FAST-NOODLE-BEEF', '桶装105g', 5.50, 6.50, 3.40, 120, 25, 180, 30]],
  },
  {
    id: 18, cid: 1, cat: 4, name: '面匠 老坛酸菜面', short: '老坛酸菜面', brand: '面匠',
    desc: '酸爽开胃的老坛酸菜汤底，桶装 120g', unit: '桶', multi: false, shape: 'cup',
    color: '#F97316', vSales: 380, sort: 90, status: 1,
    skus: [[19, 'FAST-NOODLE-PICKLE', '桶装120g', 5.50, 6.50, 3.50, 108, 25, 180, 28]],
  },
  {
    id: 19, cid: 1, cat: 4, name: '汤好鲜 骨汤面', short: '骨汤面', brand: '汤好鲜',
    desc: '浓骨汤底非油炸面饼，5 连包家庭囤货款', unit: '包', multi: false, shape: 'pack',
    color: '#FACC15', vSales: 150, sort: 80, status: 1,
    skus: [[20, 'FAST-NOODLE-5PK', '5连包', 13.90, 16.90, 9.20, 54, 12, 210, 20]],
  },
  {
    id: 20, cid: 1, cat: 4, name: '自热派 麻辣牛肉自热饭', short: '自热米饭', brand: '自热派',
    desc: '不插电自加热，15 分钟吃上热饭，麻辣牛肉口味', unit: '盒', multi: false, shape: 'box',
    color: '#B91C1C', vSales: 90, sort: 70, status: 1,
    skus: [[21, 'FAST-SELFHEAT-RICE', '260g', 19.90, 24.90, 13.20, 30, 8, 270, 18]],
  },
  {
    id: 21, cid: 1, cat: 4, name: '旺肉多 优级火腿肠', short: '火腿肠', brand: '旺肉多',
    desc: '肉含量高的即食火腿肠，60g×10 支装', unit: '组', multi: false, shape: 'stick',
    color: '#EF4444', vSales: 200, sort: 60, status: 1,
    skus: [[22, 'FAST-SAUSAGE-10', '60g×10支', 15.90, 18.90, 10.50, 66, 15, 180, 22]],
  },
  {
    id: 22, cid: 1, cat: 4, name: '脆爽 涪陵榨菜', short: '涪陵榨菜', brand: '脆爽',
    desc: '佐粥下饭的经典小菜，70g 独立鲜香装', unit: '袋', multi: false, shape: 'pouch',
    color: '#65A30D', vSales: 170, sort: 50, status: 1,
    skus: [[23, 'FAST-PICKLE-70', '70g', 3.50, 4.50, 2.00, 110, 25, 365, 40]],
  },
  {
    id: 23, cid: 1, cat: 4, name: '香林 午餐肉罐头', short: '午餐肉罐头', brand: '香林',
    desc: '厚切肉粒午餐肉，340g 铁罐，泡面搭档', unit: '罐', multi: false, shape: 'jar',
    color: '#A16207', vSales: 75, sort: 40, status: 1,
    skus: [[24, 'FAST-LUNCHEON-340', '340g', 12.90, 15.90, 8.40, 40, 10, 720, 45]],
  },

  // 乳品烘焙
  {
    id: 24, cid: 1, cat: 5, name: '怡香源 纯牛奶', short: '纯牛奶', brand: '怡香源',
    desc: '3.2g 优质乳蛋白，250ml 利乐砖整箱装', unit: '箱', multi: true, shape: 'carton',
    color: '#4F46E5', vSales: 260, sort: 100, status: 1, alt: true,
    spec: { id: 2, name: '规格', values: [[3, '12盒装'], [4, '24盒装']] },
    skus: [
      [25, 'MILK-PURE-12', '250ml×12盒', 42.90, 52.90, 33.50, 50, 12, 180, 30, true],
      [26, 'MILK-PURE-24', '250ml×24盒', 82.90, 99.90, 65.80, 24, 6, 180, 30],
    ],
  },
  {
    id: 25, cid: 1, cat: 5, name: '牧场精选 高钙牛奶', short: '高钙牛奶', brand: '牧场精选',
    desc: '强化钙配方，250ml×12 礼盒装，早晚一瓶', unit: '箱', multi: false, shape: 'carton',
    color: '#0EA5E9', vSales: 110, sort: 90, status: 1,
    skus: [[27, 'MILK-CALCIUM-12', '250ml×12盒', 59.90, 69.90, 46.50, 28, 8, 180, 26]],
  },
  {
    id: 26, cid: 1, cat: 5, name: '慕希 原味浓稠酸奶', short: '浓稠酸奶', brand: '慕希',
    desc: '浓稠口感原味发酵乳，205g×12 瓶装', unit: '箱', multi: false, shape: 'bottle',
    color: '#F472B6', vSales: 145, sort: 80, status: 1,
    skus: [[28, 'MILK-YOGURT-12', '205g×12', 49.90, 59.90, 37.20, 32, 8, 90, 20]],
  },
  {
    id: 27, cid: 1, cat: 5, name: '晨鲜 巴氏鲜牛奶', short: '巴氏鲜牛奶', brand: '晨鲜',
    desc: '低温巴氏杀菌，保留鲜奶本味，950ml 大瓶装', unit: '瓶', multi: false, shape: 'bottle',
    color: '#93C5FD', vSales: 95, sort: 70, status: 1,
    skus: [[29, 'MILK-FRESH-950', '950ml', 15.90, 18.90, 11.20, 8, 15, 3, 1]],
  },
  {
    id: 28, cid: 1, cat: 5, name: '麦香坊 法式软面包', short: '法式软面包', brand: '麦香坊',
    desc: '独立小包软面包，400g 约 8 枚，早餐随手带', unit: '袋', multi: false, shape: 'pack',
    color: '#D97706', vSales: 130, sort: 60, status: 1,
    skus: [[30, 'BAKERY-BUN-400', '400g', 12.90, 15.90, 8.20, 45, 10, 15, 4]],
  },
  {
    id: 29, cid: 1, cat: 5, name: '醇熟 切片吐司', short: '切片吐司', brand: '醇熟',
    desc: '松软吐司切片，400g 一袋，做三明治方便', unit: '袋', multi: false, shape: 'bag',
    color: '#FBBF24', vSales: 60, sort: 50, status: 0,
    skus: [[31, 'BAKERY-TOAST-400', '400g', 9.90, 12.90, 6.30, 20, 5, 10, 2]],
  },

  // 粮油调味
  {
    id: 30, cid: 1, cat: 6, name: '金稻 东北长粒香米', short: '长粒香米', brand: '金稻',
    desc: '当季新米，颗粒饱满，5kg/10kg 家庭装', unit: '袋', multi: true, shape: 'ricebag',
    color: '#CA8A04', vSales: 180, sort: 100, status: 1, alt: true,
    spec: { id: 3, name: '规格', values: [[5, '5kg'], [6, '10kg']] },
    skus: [
      [32, 'GRAIN-RICE-5KG', '5kg', 39.90, 46.90, 31.50, 50, 12, null, 20, true],
      [33, 'GRAIN-RICE-10KG', '10kg', 74.90, 89.90, 61.00, 22, 5, null, 20],
    ],
  },
  {
    id: 31, cid: 1, cat: 6, name: '油乐 压榨菜籽油', short: '菜籽油', brand: '油乐',
    desc: '物理压榨一级菜籽油，5L 桶装家用', unit: '桶', multi: false, shape: 'jug',
    color: '#EAB308', vSales: 95, sort: 90, status: 1,
    skus: [[34, 'GRAIN-OIL-5L', '5L', 79.90, 92.90, 62.50, 24, 6, 540, 35]],
  },
  {
    id: 32, cid: 1, cat: 6, name: '海天下 金标生抽', short: '金标生抽', brand: '海天下',
    desc: '零添加头道原香酱油，1.9L 大瓶装', unit: '瓶', multi: false, shape: 'bottle',
    color: '#78350F', vSales: 160, sort: 80, status: 1,
    skus: [[35, 'GRAIN-SOYSAUCE', '1.9L', 16.90, 19.90, 11.80, 38, 10, 540, 30]],
  },
  {
    id: 33, cid: 1, cat: 6, name: '海天下 五年陈醋', short: '五年陈醋', brand: '海天下',
    desc: '粮食固态发酵陈醋，500ml 瓶装', unit: '瓶', multi: false, shape: 'bottle',
    color: '#57534E', vSales: 85, sort: 70, status: 1,
    skus: [[36, 'GRAIN-VINEGAR', '500ml', 6.90, 8.90, 4.30, 46, 10, 720, 28]],
  },
  {
    id: 34, cid: 1, cat: 6, name: '白盐 精制食用盐', short: '食用盐', brand: '白盐',
    desc: '加碘精制盐，400g 袋装，厨房常备', unit: '袋', multi: false, shape: 'pack',
    color: '#E2E8F0', vSales: 240, sort: 60, status: 1,
    skus: [[37, 'GRAIN-SALT-400', '400g', 2.50, 3.00, 1.40, 90, 20, null, 40]],
  },

  // 日用百货
  {
    id: 35, cid: 1, cat: 7, name: '维纯 抽取式面巾纸', short: '抽取面巾纸', brand: '维纯',
    desc: '3 层 100 抽加厚原生木浆，6 包/12 包囤货装', unit: '提', multi: true, shape: 'tissue',
    color: '#14B8A6', vSales: 300, sort: 100, status: 1, alt: true,
    spec: { id: 4, name: '规格', values: [[7, '6包装'], [8, '12包装']] },
    skus: [
      [38, 'DAILY-TISSUE-6', '3层×6包', 12.90, 15.90, 8.90, 100, 25, null, 25, true],
      [39, 'DAILY-TISSUE-12', '3层×12包', 23.90, 29.90, 17.20, 40, 10, null, 25],
    ],
  },
  {
    id: 36, cid: 1, cat: 7, name: '清月 深层洁净洗衣液', short: '洗衣液', brand: '清月',
    desc: '低泡易漂洗，3kg 大瓶装，清新花香', unit: '瓶', multi: false, shape: 'jug',
    color: '#3B82F6', vSales: 120, sort: 90, status: 1,
    skus: [[40, 'DAILY-LAUNDRY-3KG', '3kg', 29.90, 36.90, 20.50, 26, 6, 1080, 30]],
  },
  {
    id: 37, cid: 1, cat: 7, name: '净立 柠檬洗洁精', short: '柠檬洗洁精', brand: '净立',
    desc: '强力去油易冲洗，1.5kg 柠檬香型', unit: '瓶', multi: false, shape: 'bottle',
    color: '#84CC16', vSales: 140, sort: 80, status: 1,
    skus: [[41, 'DAILY-DISHSOAP', '1.5kg', 12.90, 15.90, 8.30, 34, 8, 1080, 26]],
  },
  {
    id: 38, cid: 1, cat: 7, name: '草本 薄荷牙膏', short: '薄荷牙膏', brand: '草本',
    desc: '清新薄荷口味，120g 家庭装', unit: '支', multi: false, shape: 'tube',
    color: '#10B981', vSales: 105, sort: 70, status: 1,
    skus: [[42, 'DAILY-PASTE-120', '120g', 15.90, 19.90, 9.80, 40, 10, 1080, 32]],
  },

  // 冷冻食品
  {
    id: 39, cid: 1, cat: 8, name: '三鲜 猪肉白菜水饺', short: '猪肉白菜水饺', brand: '三鲜',
    desc: '皮薄馅大的冷冻水饺，500g/1kg 家庭装', unit: '袋', multi: true, shape: 'box',
    color: '#0284C7', vSales: 190, sort: 100, status: 1, alt: true,
    spec: { id: 5, name: '规格', values: [[9, '500g'], [10, '1kg']] },
    skus: [
      [43, 'FROZEN-DUMPLING-500', '500g', 12.90, 15.90, 8.60, 35, 10, 365, 15, true],
      [44, 'FROZEN-DUMPLING-1KG', '1kg', 21.90, 26.90, 15.40, 18, 5, 365, 15],
    ],
  },
  {
    id: 40, cid: 1, cat: 8, name: '芝麻心 黑芝麻汤圆', short: '黑芝麻汤圆', brand: '芝麻心',
    desc: '糯叽叽的黑芝麻流心汤圆，500g 约 32 颗', unit: '袋', multi: false, shape: 'box',
    color: '#F59E0B', vSales: 150, sort: 90, status: 1,
    skus: [[45, 'FROZEN-TANGYUAN', '500g', 9.90, 12.90, 6.40, 40, 10, 365, 18]],
  },
  {
    id: 41, cid: 1, cat: 8, name: '雪绒 巧克力脆皮雪糕', short: '脆皮雪糕', brand: '雪绒',
    desc: '脆皮裹香草冰淇淋，75g 单支装', unit: '支', multi: false, shape: 'stick',
    color: '#7C3AED', vSales: 260, sort: 80, status: 1,
    skus: [[46, 'FROZEN-ICECREAM', '75g', 3.50, 4.50, 1.90, 0, 20, 540, 10]],
  },

  // 糖果巧克力
  {
    id: 42, cid: 1, cat: 9, name: '丝滑 牛奶巧克力', short: '牛奶巧克力', brand: '丝滑',
    desc: '入口丝滑的牛奶巧克力，43g 排块装', unit: '块', multi: false, shape: 'bar',
    color: '#92400E', vSales: 175, sort: 100, status: 1,
    skus: [[47, 'CANDY-CHOCO-43', '43g', 8.50, 9.90, 5.30, 60, 15, 365, 22]],
  },
  {
    id: 43, cid: 1, cat: 9, name: '能量棒 花生夹心巧克力', short: '花生能量棒', brand: '能量棒',
    desc: '花生牛糖夹心，扛饿补给，51g 单支装', unit: '支', multi: false, shape: 'bar',
    color: '#B45309', vSales: 210, sort: 90, status: 1,
    skus: [[48, 'CANDY-BAR-51', '51g', 6.50, 7.50, 3.90, 72, 18, 300, 20]],
  },
  {
    id: 44, cid: 1, cat: 9, name: '清息 无糖口香糖', short: '无糖口香糖', brand: '清息',
    desc: '薄荷味无糖口香糖，64g 瓶装，随时清新', unit: '瓶', multi: false, shape: 'jar',
    color: '#22D3EE', vSales: 90, sort: 80, status: 1,
    skus: [[49, 'CANDY-GUM-64', '64g', 9.90, 11.90, 6.10, 6, 10, 540, 25]],
  },

  // 冲调饮品
  {
    id: 45, cid: 1, cat: 10, name: '雀语 特浓速溶咖啡', short: '速溶咖啡', brand: '雀语',
    desc: '三合一特浓配方，15g×30 条独立装', unit: '盒', multi: false, shape: 'box',
    color: '#78350F', vSales: 130, sort: 100, status: 1,
    skus: [[50, 'BREW-COFFEE-30', '15g×30条', 39.90, 46.90, 28.60, 30, 8, 540, 28]],
  },
  {
    id: 46, cid: 1, cat: 10, name: '飘飘 芋圆奶茶', short: '芋圆奶茶', brand: '飘飘',
    desc: '冷热皆宜的杯装奶茶，80g 加料版', unit: '杯', multi: false, shape: 'cup',
    color: '#A855F7', vSales: 165, sort: 90, status: 1,
    skus: [[51, 'BREW-MILKTEA-80', '80g', 6.50, 7.90, 3.80, 54, 12, 360, 20]],
  },
  {
    id: 47, cid: 1, cat: 10, name: '麦谷 即食燕麦片', short: '即食燕麦片', brand: '麦谷',
    desc: '澳洲燕麦，热水一冲即食，400g 罐装', unit: '罐', multi: false, shape: 'jar',
    color: '#D6D3D1', vSales: 75, sort: 80, status: 1,
    skus: [[52, 'BREW-OATS-400', '400g', 19.90, 24.90, 13.20, 26, 6, 360, 24]],
  },

  // ===== 社区2：阳光花园 =====
  {
    id: 4, cid: 2, cat: 3, name: '农欢 散养鲜鸡蛋', short: '散养鲜鸡蛋', brand: '农欢',
    desc: '社区农户散养，30 枚防震蛋托装', unit: '箱', multi: false, shape: 'eggcarton',
    color: '#D97706', vSales: 120, sort: 100, status: 1,
    skus: [[5, 'EGG-30P', '30枚/箱', 12.90, 15.90, 9.50, 100, 20, 15, 24, true]],
  },
  {
    id: 48, cid: 2, cat: 3, name: '怡香源 纯牛奶', short: '纯牛奶', brand: '怡香源',
    desc: '3.2g 优质乳蛋白，250ml×12 整箱装', unit: '箱', multi: false, shape: 'carton',
    color: '#4F46E5', vSales: 60, sort: 90, status: 1, reuse: 'milk-carton.png',
    skus: [[53, 'MILK-PURE-C2-12', '250ml×12盒', 45.90, 52.90, 35.80, 24, 6, 180, 22]],
  },
  {
    id: 49, cid: 2, cat: 3, name: '慕希 原味浓稠酸奶', short: '浓稠酸奶', brand: '慕希',
    desc: '浓稠口感原味发酵乳，205g×12 瓶装', unit: '箱', multi: false, shape: 'bottle',
    color: '#F472B6', vSales: 48, sort: 80, status: 1, reuse: 'yogurt-bottle.png',
    skus: [[54, 'MILK-YOGURT-C2-12', '205g×12', 52.90, 59.90, 39.80, 18, 5, 90, 15]],
  },
  {
    id: 50, cid: 2, cat: 11, name: '乐游 原味薯片', short: '原味薯片', brand: '乐游',
    desc: '厚切马铃薯片，咸香酥脆，70g 便携装', unit: '袋', multi: false, shape: 'bag',
    color: '#F59E0B', vSales: 88, sort: 100, status: 1, reuse: 'chips.png',
    skus: [[55, 'SNACK-CHIPS-C2-70', '70g', 5.90, 6.90, 3.20, 60, 15, 240, 18]],
  },
  {
    id: 51, cid: 2, cat: 11, name: '麦香源 巧克力夹心饼干', short: '夹心饼干', brand: '麦香源',
    desc: '酥脆饼干夹可可奶油心，116g 小袋装', unit: '袋', multi: false, shape: 'pack',
    color: '#8B5CF6', vSales: 42, sort: 90, status: 1, reuse: 'cookie.png',
    skus: [[56, 'SNACK-COOKIE-C2-116', '116g', 7.20, 8.50, 4.40, 40, 10, 210, 16]],
  },
  {
    id: 52, cid: 2, cat: 12, name: '碧源 饮用天然水', short: '饮用天然水', brand: '碧源',
    desc: '弱碱性天然水，550ml 随身装', unit: '瓶', multi: false, shape: 'bottle',
    color: '#0EA5E9', vSales: 150, sort: 100, status: 1, reuse: 'water.png',
    skus: [[57, 'DRINK-WATER-C2-550', '550ml', 2.00, 2.50, 1.25, 96, 24, 720, 30]],
  },
  {
    id: 53, cid: 2, cat: 12, name: '气泡山 柠檬味汽水', short: '柠檬汽水', brand: '气泡山',
    desc: '清爽柠檬口味汽水，500ml 大瓶装', unit: '瓶', multi: false, shape: 'bottle',
    color: '#22C55E', vSales: 66, sort: 90, status: 1, reuse: 'sprite.png',
    skus: [[58, 'DRINK-SPRITE-C2-500', '500ml', 3.80, 4.00, 2.20, 48, 12, 300, 22]],
  },
  {
    id: 54, cid: 2, cat: 13, name: '面匠 红烧牛肉面', short: '红烧牛肉面', brand: '面匠',
    desc: '经典红烧汤底，桶装免洗碗', unit: '桶', multi: false, shape: 'cup',
    color: '#DC2626', vSales: 110, sort: 100, status: 1, reuse: 'noodle-cup.png',
    skus: [[59, 'FAST-NOODLE-C2-BEEF', '桶装105g', 5.80, 6.50, 3.50, 45, 12, 180, 20]],
  },
  {
    id: 55, cid: 2, cat: 14, name: '金稻 东北长粒香米', short: '长粒香米', brand: '金稻',
    desc: '当季新米，颗粒饱满，5kg 家庭装', unit: '袋', multi: false, shape: 'ricebag',
    color: '#CA8A04', vSales: 36, sort: 100, status: 1, reuse: 'rice-bag.png',
    skus: [[60, 'GRAIN-RICE-C2-5KG', '5kg', 42.90, 46.90, 33.80, 18, 5, null, 18]],
  },
  {
    id: 56, cid: 2, cat: 15, name: '维纯 抽取式面巾纸', short: '抽取面巾纸', brand: '维纯',
    desc: '3 层 100 抽原生木浆，6 包装', unit: '提', multi: false, shape: 'tissue',
    color: '#14B8A6', vSales: 54, sort: 100, status: 1, reuse: 'tissue-pack.png',
    skus: [[61, 'DAILY-TISSUE-C2-6', '3层×6包', 13.50, 15.90, 9.20, 30, 8, null, 20]],
  },
];

// ---------- 工具 ----------
const money = (v) => v.toFixed(2);
const q = (s) => `'${String(s).replace(/'/g, "''")}'`;

// EAN-13 校验位：前 12 位 → 追加校验码
function ean13(base12) {
  const d = base12.split('').map(Number);
  const sum = d.reduce((acc, v, i) => acc + v * (i % 2 === 0 ? 1 : 3), 0);
  return base12 + String((10 - (sum % 10)) % 10);
}

const byId = new Map(products.map((p) => [p.id, p]));
const skuOwner = new Map();
for (const p of products) for (const s of p.skus) skuOwner.set(s[0], p);

// 直接用英文语义 slug 作为文件名，便于人工核对
const FILE_SLUG = {
  1: 'latiao', 3: 'chips', 5: 'cookie', 6: 'nuts', 7: 'jerky', 8: 'snowcake', 9: 'konjac',
  2: 'water', 10: 'cola', 11: 'sprite', 12: 'iced-tea', 13: 'oolong', 14: 'energy-drink', 15: 'beer', 16: 'lactic-drink',
  17: 'noodle-cup', 18: 'noodle-pickle-cup', 19: 'noodle-5pack', 20: 'selfheat-rice', 21: 'sausage', 22: 'pickle', 23: 'luncheon-jar',
  24: 'milk-carton', 25: 'milk-calcium', 26: 'yogurt-bottle', 27: 'fresh-milk', 28: 'soft-bread', 29: 'toast',
  30: 'rice-bag', 31: 'oil-jug', 32: 'soy-sauce', 33: 'vinegar', 34: 'salt',
  35: 'tissue-pack', 36: 'laundry-jug', 37: 'dish-soap', 38: 'toothpaste',
  39: 'dumpling-box', 40: 'tangyuan', 41: 'icecream',
  42: 'chocolate', 43: 'energy-bar', 44: 'gum-jar',
  45: 'coffee-box', 46: 'milktea-cup', 47: 'oats-jar',
  4: 'egg-carton',
};
const IMG_OF = {};
for (const p of products) {
  const base = p.reuse ? p.reuse.replace('.png', '') : FILE_SLUG[p.id];
  IMG_OF[p.id] = `${IMG_DIR}/${base}.png`;
  if (p.alt) IMG_OF[p.id + '-alt'] = `${IMG_DIR}/${base}-alt.png`;
}

// 待绘制图片清单：以 FILE_SLUG 为准（与 IMG_OF 使用同一批文件名），社区2 复用社区1 的图不再重复出条目
const images = [];
const seenFile = new Set();
function pushImage(file, p, note) {
  if (seenFile.has(file)) return;
  seenFile.add(file);
  images.push({ file, label: p.short, brand: p.brand, shape: p.shape, color: p.color, note: note || '' });
}
for (const p of products) {
  if (p.reuse) continue;
  const base = FILE_SLUG[p.id];
  if (!base) throw new Error(`商品 ${p.id} 缺少 FILE_SLUG`);
  pushImage(`${base}.png`, p, '');
  if (p.alt) pushImage(`${base}-alt.png`, p, 'alt');
}

// 分类图标
const CAT_ICON = {};
for (const c of categories) {
  const first = products.filter((p) => p.cat === c.id)[0];
  CAT_ICON[c.id] = IMG_OF[first.id];
}

// ---------- 生成 SQL ----------
const L = [];
L.push('-- ============ 商品分类 ============');
L.push('-- 社区1（盘龙金茂悦）9 类、社区2（阳光花园）6 类，均为一级分类：');
L.push('-- C 端分类页只取 parent_id=0 且 status=1 的分类，商品直接挂在一级分类下');
L.push('-- icon 复用该分类首个商品的图片，避免再引入一套图标素材');
L.push('INSERT INTO `categories` (`id`, `community_id`, `parent_id`, `name`, `icon`, `sort`, `status`, `created_at`, `updated_at`)');
L.push('VALUES');
L.push(categories.map((c) => `  (${c.id}, ${c.cid}, 0, ${q(c.name)}, ${q(CAT_ICON[c.id])}, ${c.sort}, 1, NOW(), NOW())`).join(',\n') + ';');
L.push('');

L.push('-- ============ 商品 ============');
L.push('-- 汇总口径：min_price/max_price = SKU 价格区间；min_original_price = 最低价 SKU 的划线价（0 不展示）；');
L.push('-- stock = 各启用 SKU 库存之和；展示销量 = sales_count + virtual_sales');
L.push('-- 商品 1/24/30/35/39 为多规格，其余单规格；29 号为仓库中（status=0）、41 号为售罄（stock=0）、');
L.push('-- 27/44 号为库存紧张（stock ≤ 安全库存），用于演示商品列表四个状态页签');
L.push('INSERT INTO `products` (`id`, `community_id`, `category_id`, `name`, `description`, `cover_image`, `images`, `unit`, `is_multi_spec`, `min_price`, `max_price`, `min_original_price`, `show_sales`, `virtual_sales`, `stock`, `min_buy`, `status`, `sort`, `sales_count`, `created_at`, `updated_at`)');
L.push('VALUES');
const prodRows = [];
for (const p of products) {
  const sorted = [...p.skus].sort((a, b) => a[3] - b[3]);
  const min = sorted[0][3];
  const max = sorted[sorted.length - 1][3];
  const minOrig = sorted[0][4];
  const stock = p.skus.reduce((s, x) => s + x[6], 0);
  const imgs = [IMG_OF[p.id]];
  if (p.alt) imgs.push(IMG_OF[p.id + '-alt']);
  prodRows.push(
    `  (${p.id}, ${p.cid}, ${p.cat}, ${q(p.name)}, ${q(p.desc)}, ${q(imgs[0])}, ${q(JSON.stringify(imgs))}, ${q(p.unit)}, ${p.multi ? 1 : 0}, ${money(min)}, ${money(max)}, ${money(minOrig)}, 1, ${p.vSales}, ${stock}, 1, ${p.status}, ${p.sort}, 0, NOW(), NOW())`,
  );
}
L.push(prodRows.join(',\n') + ';');
L.push('');

L.push('-- 商品图集：与 products.images 对应（多规格商品给一张正面 + 一张细节图）');
L.push('INSERT INTO `product_images` (`id`, `product_id`, `url`, `sort`, `created_at`)');
L.push('VALUES');
const imgRows = [];
let imgId = 1;
for (const p of products) {
  imgRows.push(`  (${imgId++}, ${p.id}, ${q(IMG_OF[p.id])}, 0, NOW())`);
  if (p.alt) imgRows.push(`  (${imgId++}, ${p.id}, ${q(IMG_OF[p.id + '-alt'])}, 1, NOW())`);
}
L.push(imgRows.join(',\n') + ';');
L.push('');

L.push('-- ============ 多规格链路（5 个多规格商品） ============');
L.push('-- sku.spec_value_ids 为规格值 ID 组合键（逗号分隔、升序），spec_json 为展示快照');
L.push('INSERT INTO `product_specs` (`id`, `product_id`, `community_id`, `name`, `sort`, `created_at`, `updated_at`, `deleted_at`)');
L.push('VALUES');
const specRows = [];
const valueRows = [];
for (const p of products) {
  if (!p.spec) continue;
  specRows.push(`  (${p.spec.id}, ${p.id}, ${p.cid}, ${q(p.spec.name)}, 0, NOW(), NOW(), NULL)`);
  let s = 1;
  for (const [vid, vname] of p.spec.values) {
    valueRows.push(`  (${vid}, ${p.spec.id}, ${p.id}, ${p.cid}, ${q(vname)}, ${s++}, NOW(), NOW(), NULL)`);
  }
}
L.push(specRows.join(',\n') + ';');
L.push('');
L.push('INSERT INTO `product_spec_values` (`id`, `spec_id`, `product_id`, `community_id`, `name`, `sort`, `created_at`, `updated_at`, `deleted_at`)');
L.push('VALUES');
L.push(valueRows.join(',\n') + ';');
L.push('');

L.push('-- ============ SKU ============');
L.push('-- barcode 为 EAN-13（含校验位）且社区内唯一，供收银台扫码与 C 端条码搜索使用');
L.push('INSERT INTO `product_skus` (`id`, `product_id`, `community_id`, `sku_code`, `spec_value_ids`, `spec_name`, `spec_json`, `price`, `original_price`, `cost_price`, `barcode`, `stock`, `safety_stock`, `image`, `status`, `created_at`, `updated_at`)');
L.push('VALUES');
const skuRows = [];
let barcodeSeq = 0;
const BARCODE_OF = {};
for (const p of products) {
  for (const s of p.skus) {
    barcodeSeq += 1;
    BARCODE_OF[s[0]] = ean13('6901' + String(barcodeSeq).padStart(8, '0'));
  }
}
for (const p of products) {
  for (const s of p.skus) {
    const [id, code, specName, price, orig, cost, stock, safety] = s;
    const vid = p.spec ? p.spec.values[p.skus.indexOf(s)][0] : null;
    const specJson = JSON.stringify({ [p.spec ? p.spec.name : '规格']: specName });
    skuRows.push(
      `  (${id}, ${p.id}, ${p.cid}, ${q(code)}, ${q(vid ? String(vid) : '')}, ${q(specName)}, ${q(specJson)}, ${money(price)}, ${money(orig)}, ${money(cost)}, ${q(BARCODE_OF[id])}, ${stock}, ${safety}, ${q(IMG_OF[p.id])}, 1, NOW(), NOW())`,
    );
  }
}
L.push(skuRows.join(',\n') + ';');
L.push('');

L.push('-- ============ 批次库存（FEFO 基础数据） ============');
L.push('-- 各 SKU 批次 quantity 之和等于 product_skus.stock；否则订单扣减走 FEFO 会因');
L.push('-- 「批次库存不足」失败（销售扣减只认 stock_batches）。');
L.push('-- 日期一律用相对时间：预包装食品给 3~24 个月保质期，部分留空（盐/大米/纸品不限期），');
L.push('-- 鲜奶与临期批次给 2~3 天，用于演示临期预警与 FEFO 顺序，避免种子过期后演示失真。');
L.push('-- split 标记的 SKU 拆两批（6:4，先出近效期批），其余单批。');
L.push('-- MANUAL_BATCHES 里的 SKU 用手写批次，与下方「进销存」段落的入库单/报损单/库存流水咬合：');
L.push('-- sku 4（乐游 原味薯片）初始 30/20 → 入库 +50（IN-SEED-0001）→ 报损 10（扣 A 批），终态 20/20/50 = 90。');
L.push('INSERT INTO `stock_batches` (`id`, `community_id`, `sku_id`, `batch_no`, `production_date`, `expire_date`, `quantity`, `unit_cost`, `status`, `source_type`, `ref_no`, `created_at`, `updated_at`)');
L.push('VALUES');
// 手写批次：与数据包「进销存样本」段（gen-data-package.mjs 的 stock_in/stock_out/stock_records）
// 互相咬合，数量之和必须等于该 SKU 的 stock。
// sku 4（乐游 原味薯片，社区1）：初始两批 30/20 → 5 天前直接入库 +50（IN-SEED-0001）→ 3 天前报损 10（扣 A 批，余 20）
// 生成后会打印这些批次号拿到的自增 id，供 stock_in_order_items.batch_id / stock_records.batch_id 回填。
const MANUAL_BATCHES = {
  4: [
    { qty: 20, no: 'INIT-SNACK-CHIPS-70-A', prodAgo: 25, expireIn: 215, ref: 'INIT-SEED', ago: 25, remark: '初始库存（已扣报损 10）' },
    { qty: 20, no: 'INIT-SNACK-CHIPS-70-B', prodAgo: 20, expireIn: 220, ref: 'INIT-SEED', ago: 20, remark: '初始库存' },
    { qty: 50, no: 'IN-SEED-0001', prodAgo: 5, expireIn: 235, ref: 'IN-SEED-0001', ago: 5, remark: '直接入库单批次' },
  ],
};

const batchRows = [];
const BATCH_ID_OF = {};
let batchId = 1;
for (const p of products) {
  for (const s of p.skus) {
    const [id, code, , , , cost, stock, , expire, prodAgo, split] = s;
    if (stock === 0) continue;
    const expExpr = (offset) =>
      expire === null ? 'NULL' : `DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL ${expire + offset} DAY), '%Y-%m-%d')`;
    const prodExpr = (ago) => `DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL ${ago} DAY), '%Y-%m-%d')`;
    const manual = MANUAL_BATCHES[id];
    if (manual) {
      for (const m of manual) {
        BATCH_ID_OF[m.no] = batchId;
        batchRows.push(
          // 备注写成行首注释：行尾注释会把 join 追加的分号/逗号一起吃掉
          `  -- ${m.remark}\n  (${batchId}, ${p.cid}, ${id}, ${q(m.no)}, ${prodExpr(m.prodAgo)}, ${m.expireIn === null ? 'NULL' : `DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL ${m.expireIn} DAY), '%Y-%m-%d')`}, ${m.qty}, ${money(cost)}, 1, 1, ${q(m.ref)}, DATE_SUB(NOW(), INTERVAL ${m.ago} DAY), DATE_SUB(NOW(), INTERVAL ${m.ago} DAY))`,
        );
        batchId += 1;
      }
      continue;
    }
    if (split) {
      const a = Math.ceil(stock * 0.6);
      const b = stock - a;
      batchRows.push(
        `  (${batchId}, ${p.cid}, ${id}, ${q('INIT-' + code + '-A')}, ${prodExpr(prodAgo + 12)}, ${expExpr(-6)}, ${a}, ${money(cost)}, 1, 1, 'INIT-SEED', NOW(), NOW())`,
      );
      batchId += 1;
      batchRows.push(
        `  (${batchId}, ${p.cid}, ${id}, ${q('INIT-' + code + '-B')}, ${prodExpr(prodAgo)}, ${expExpr(0)}, ${b}, ${money(cost)}, 1, 1, 'INIT-SEED', NOW(), NOW())`,
      );
      batchId += 1;
    } else {
      batchRows.push(
        `  (${batchId}, ${p.cid}, ${id}, ${q('INIT-' + code + '-A')}, ${prodExpr(prodAgo)}, ${expExpr(0)}, ${stock}, ${money(cost)}, 1, 1, 'INIT-SEED', NOW(), NOW())`,
      );
      batchId += 1;
    }
  }
}
L.push(batchRows.join(',\n') + ';');
L.push('');

// ---------- 结构化数据导出（供 gen-data-package.mjs 等消费，无副作用） ----------
// 批次推导与下方 SQL 生成同源同公式：sku 元组 [id, code, specName, price, original, cost, stock, safety, expireDays, prodAgo, split?]
// community_key 口径：源数据用 cid（1=盘龙金茂悦 / 2=阳光花园）标注归属，导出为包内社区 key，
// 供数据包按社区过滤——两个社区的同名商品是各自独立的演示条目，不可灌进同一社区。
function collectSeedCatalog() {
  const barcodes = {};
  let seq = 0;
  for (const p of products) {
    for (const s of p.skus) {
      seq += 1;
      barcodes[s[0]] = ean13('6901' + String(seq).padStart(8, '0'));
    }
  }
  const imagesOf = {};
  for (const p of products) {
    const base = p.reuse ? p.reuse.replace('.png', '') : FILE_SLUG[p.id];
    if (!base) throw new Error(`商品 ${p.id} 缺少 FILE_SLUG`);
    const imgs = [`${IMG_DIR}/${base}.png`];
    if (p.alt) imgs.push(`${IMG_DIR}/${base}-alt.png`);
    imagesOf[p.id] = imgs;
  }
  const catIcon = {};
  for (const c of categories) {
    const first = products.filter((p) => p.cat === c.id)[0];
    catIcon[c.id] = imagesOf[first.id][0];
  }
  const skus = [];
  for (const p of products) {
    for (const s of p.skus) {
      const [id, code, specName, price, orig, cost, stock, safety, expire, prodAgo, split] = s;
      let batches = [];
      if (stock > 0) {
        const manual = MANUAL_BATCHES[id];
        if (manual) {
          batches = manual.map((m) => ({ no: m.no, production_ago: m.prodAgo, expire_in: m.expireIn, quantity: m.qty, unit_cost: cost, ref: m.ref }));
        } else if (split) {
          const a = Math.ceil(stock * 0.6);
          batches = [
            { no: `INIT-${code}-A`, production_ago: prodAgo + 12, expire_in: expire === null ? null : expire - 6, quantity: a, unit_cost: cost, ref: 'INIT-SEED' },
            { no: `INIT-${code}-B`, production_ago: prodAgo, expire_in: expire, quantity: stock - a, unit_cost: cost, ref: 'INIT-SEED' },
          ];
        } else {
          batches = [{ no: `INIT-${code}-A`, production_ago: prodAgo, expire_in: expire, quantity: stock, unit_cost: cost, ref: 'INIT-SEED' }];
        }
      }
      skus.push({
        key: `sku-${id}`, product_key: `prod-${p.id}`, code, spec_name: specName,
        spec_value_key: p.spec ? `sv-${p.spec.values[p.skus.indexOf(s)][0]}` : '',
        price, original_price: orig, cost_price: cost, barcode: barcodes[id],
        stock, safety_stock: safety, image: imagesOf[p.id][0], status: 1,
        batches,
      });
    }
  }
  return {
    categories: categories.map((c) => ({ key: `cat-${c.id}`, community_key: `comm-${c.cid}`, name: c.name, sort: c.sort, icon: catIcon[c.id] })),
    products: products.map((p) => ({
      key: `prod-${p.id}`, category_key: `cat-${p.cat}`, community_key: `comm-${p.cid}`, name: p.name, description: p.desc,
      unit: p.unit, is_multi_spec: !!p.multi, sort: p.sort, virtual_sales: p.vSales, status: p.status,
      images: imagesOf[p.id],
      spec: p.spec
        ? { name: p.spec.name, values: p.spec.values.map(([vid, vname]) => ({ key: `sv-${vid}`, name: vname })) }
        : null,
      sku_keys: p.skus.map((s) => `sku-${s[0]}`),
    })),
    skus,
  };
}

// SQL 组装保持顶层纯计算（import 复用数据时不执行任何写入/输出）
const SQL_OUTPUT = L.join('\n');

// ---------- 输出（仅直接运行本脚本时执行，被 import 时不产生副作用） ----------
function main() {
  process.stdout.write(SQL_OUTPUT + '\n');
  fs.writeFileSync(path.join(ASSET_DIR, 'images.json'), JSON.stringify(images, null, 2) + '\n');
  // 快照仅供人工核对 gen-data-package.mjs 里引用目录的样本段（订单/购物车/挂单中的品名、规格、图片、条码）
fs.writeFileSync(
  path.join(os.tmpdir(), 'seed-catalog-snapshot.json'),
  JSON.stringify(
    {
      products: Object.fromEntries(products.map((p) => [p.id, { name: p.name, img: IMG_OF[p.id] }])),
      skus: Object.fromEntries(
        products.flatMap((p) => p.skus.map((s) => [s[0], {
          product: p.id,
          name: p.name,
          spec: s[2],
          price: s[3],
          img: IMG_OF[p.id],
          barcode: BARCODE_OF[s[0]],
        }])),
      ),
    },
    null,
    2,
  ),
);

console.error('products:', products.length, 'skus:', barcodeSeq, 'batches:', batchId - 1, 'images:', images.length);
for (const [no, id] of Object.entries(BATCH_ID_OF)) {
  console.error(`下游引用的批次 id：${no} → stock_batches.id=${id}`);
}
console.error('快照:', path.join(os.tmpdir(), 'seed-catalog-snapshot.json'));
}

// 直接运行才生成 SQL 与图片清单；被 import（gen-data-package.mjs）时只取数据不产生副作用
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}

export { categories, products, FILE_SLUG, collectSeedCatalog };
