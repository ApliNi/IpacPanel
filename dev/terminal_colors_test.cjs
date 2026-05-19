// terminal-colors-test.cjs
const reset = '\x1b[0m';

const styles = {
 bold: '\x1b[1m', dim: '\x1b[2m', italic: '\x1b[3m', underline: '\x1b[4m',
 blink: '\x1b[5m', reverse: '\x1b[7m', strikethrough: '\x1b[9m',
};

const fg = {
 black: '\x1b[30m', red: '\x1b[31m', green: '\x1b[32m', yellow: '\x1b[33m',
 blue: '\x1b[34m', magenta: '\x1b[35m', cyan: '\x1b[36m', white: '\x1b[37m',
 brightBlack: '\x1b[90m', brightRed: '\x1b[91m', brightGreen: '\x1b[92m',
 brightYellow: '\x1b[93m', brightBlue: '\x1b[94m', brightMagenta: '\x1b[95m',
 brightCyan: '\x1b[96m', brightWhite: '\x1b[97m',
};

const bg = {
 black: '\x1b[40m', red: '\x1b[41m', green: '\x1b[42m', yellow: '\x1b[43m',
 blue: '\x1b[44m', magenta: '\x1b[45m', cyan: '\x1b[46m', white: '\x1b[47m',
 brightBlack: '\x1b[100m', brightRed: '\x1b[101m', brightGreen: '\x1b[102m',
 brightYellow: '\x1b[103m', brightBlue: '\x1b[104m', brightMagenta: '\x1b[105m',
 brightCyan: '\x1b[106m', brightWhite: '\x1b[107m',
};

console.log('\n=== 文字样式 ===');
for (const [name, code] of Object.entries(styles)) {
 console.log(`${code}${name}${reset}`);
}

console.log('\n=== 前景色 ===');
for (const [name, code] of Object.entries(fg)) {
 process.stdout.write(`${code}${name.padEnd(15)}${reset}`);
}
console.log();

console.log('\n=== 背景色 ===');
for (const [name, code] of Object.entries(bg)) {
 process.stdout.write(`${code}${name.padEnd(15)}${reset}`);
}
console.log();

console.log('\n=== 256色 前景 ===');
for (let i = 0; i < 256; i++) {
 process.stdout.write(`\x1b[38;5;${i}m${String(i).padStart(4)}${reset}`);
 if ((i + 1) % 16 === 0) console.log();
}

console.log('\n=== 256色 背景 ===');
for (let i = 0; i < 256; i++) {
 process.stdout.write(`\x1b[48;5;${i}m    ${reset}`);
 if ((i + 1) % 16 === 0) console.log();
}

console.log('\n=== RGB 真彩色示例 ===');
for (let r = 0; r <= 255; r += 51) {
 for (let g = 0; g <= 255; g += 51) {
   process.stdout.write(`\x1b[48;2;${r};${g};128m  ${reset}`);
 }
}
console.log('\n');