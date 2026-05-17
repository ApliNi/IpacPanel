// stdio-bench-overlay.js (Node.js CommonJS)
const cols = process.stdout.columns || 80;
const rows = process.stdout.rows || 24;

// 预生成填充行（用于视觉占位，不影响测试统计）
const fillLine = '#'.repeat(cols);
const fillLines = (fillLine + '\n').repeat(rows - 1);

let totalBytes = 0;
const startTime = Date.now();
let elapsed = 0;

// 初始清屏
process.stdout.write('\x1b[2J\x1b[H');

while (true) {
  const now = Date.now();
  elapsed = (now - startTime) / 1000;

  // 计算当前吞吐量
  const throughput = totalBytes / elapsed;
  const mbps = (throughput / (1024 * 1024)).toFixed(2);
  const statusLine = 
    `Elapsed: ${elapsed.toFixed(2)}s  Bytes: ${totalBytes}  Throughput: ${throughput.toFixed(0)} B/s (${mbps} MB/s)`
    .padEnd(cols, ' ');

  // 构造全屏内容：清屏 + 光标归位 + 填充行 + 状态行
  const screen = `\x1b[2J\x1b[H${fillLines}${statusLine}`;
  process.stdout.write(screen);
  
  // 累加本次写入的字节数
  totalBytes += Buffer.byteLength(screen, 'utf8');
}

// 最终统计
elapsed = (Date.now() - startTime) / 1000;
console.log('\n\n测试结束');
console.log(`总写入字节: ${totalBytes}`);
console.log(`用时: ${elapsed.toFixed(2)} 秒`);
console.log(`吞吐量: ${(totalBytes / elapsed / (1024 * 1024)).toFixed(2)} MB/s`);
process.exit(0);