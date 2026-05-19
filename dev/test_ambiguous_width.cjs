// test-ambiguous-width.cjs

const chars = [
 // 罗马数字 (U+2160+)
 { label: 'Roman Numerals', chars: 'Ⅰ Ⅱ Ⅲ Ⅳ Ⅴ Ⅵ Ⅶ Ⅷ Ⅸ Ⅹ Ⅺ Ⅻ' },
 // 希腊字母
 { label: 'Greek Letters', chars: 'α β γ δ ε ζ η θ ι κ λ μ' },
 // 制表符/框线符号
 { label: 'Box Drawing', chars: '─ │ ┌ ┐ └ ┘ ├ ┤ ┬ ┴ ┼' },
 // 数学符号
 { label: 'Math Symbols', chars: '∀ ∂ ∃ ∅ ∇ ∈ ∉ ∋ ∏ ∑ √ ∞' },
 // 箭头
 { label: 'Arrows', chars: '← ↑ → ↓ ↔ ↕ ⇐ ⇑ ⇒ ⇓ ⇔' },
 // 货币符号
 { label: 'Currency', chars: '€ £ ¥ ¢ ₩ ₹ ₿' },
 // 版权/商标
 { label: 'Misc', chars: '© ® ™ § ¶ † ‡ • …' },
];

const reset = '\x1b[0m';
const cyan = '\x1b[36m';
const yellow = '\x1b[33m';
const green = '\x1b[32m';

console.log(`\n${cyan}=== 模糊宽度字符渲染测试 ===${reset}`);
console.log('规则: 若字符超出单元格边界，xterm 的 rescaleOverlappingGlyphs 会水平压缩它\n');

// 对照列：用 ASCII 字符标定单元格宽度
const ruler = '|' + '123456789+'.repeat(5) + '|';
console.log(`${yellow}标尺: ${ruler}${reset}`);
console.log(`${yellow}对照: |a b c d e f g h i j k l m n o p q r s t|${reset}\n`);

for (const group of chars) {
 console.log(`${green}[${group.label}]${reset}`);
 console.log(`  字符: ${group.chars}`);

 // 每个字符单独一列，旁边跟 ASCII 对照
 process.stdout.write('  单列: ');
 for (const ch of [...group.chars].filter(c => c !== ' ')) {
   process.stdout.write(`${ch}|`);
 }
 console.log('\n  对照: ' + 'x|'.repeat([...group.chars].filter(c => c !== ' ').length));
 console.log();
}

// GB18030 罗马数字重点测试
console.log(`${cyan}=== GB18030 罗马数字重点对比 ===${reset}`);
const romans = ['Ⅰ','Ⅱ','Ⅲ','Ⅳ','Ⅴ','Ⅵ','Ⅶ','Ⅷ','Ⅸ','Ⅹ'];
console.log('罗马: ' + romans.join(' '));
console.log('ASCII: ' + romans.map((_, i) => i + 1).join(' '));
console.log('\n若开启 rescaleOverlappingGlyphs，罗马数字应与 ASCII 数字对齐。');
console.log('若未开启，罗马数字会溢出格子，挤压后续字符。\n');
