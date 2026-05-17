const readline = require('readline');
const tty = require('tty');

// 确保能捕获按键
readline.emitKeypressEvents(process.stdin);

// 尝试设置 raw mode（兼容各版本）
if (typeof process.stdin.setRawMode === 'function') {
  process.stdin.setRawMode(true);
} else if (tty && typeof tty.setRawMode === 'function') {
  tty.setRawMode(process.stdin.fd, true);
}
process.stdin.resume();

let startTime = Date.now();
let accumulated = 0;
let running = true;
let timer = null;

function formatTime(ms) {
  const totalSeconds = Math.floor(ms / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  const milliseconds = ms % 1000;
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}.${String(milliseconds).padStart(3, '0')}`;
}

function updateDisplay() {
  const elapsed = running ? accumulated + (Date.now() - startTime) : accumulated;
  process.stdout.write(`\r${formatTime(elapsed)}   [s:暂停/继续  r:重置  q:退出]`);
}

function stopTimer() {
  if (timer) {
    clearInterval(timer);
    timer = null;
  }
}

function startTimer() {
  stopTimer();
  timer = setInterval(updateDisplay, 10);
}

startTimer();
console.log('毫秒秒表已启动，自动计时...');
console.log('操作: s-暂停/继续  r-重置  q-退出');

process.stdin.on('keypress', (str, key) => {
  if (key.ctrl && key.name === 'c') {
    stopTimer();
    process.stdout.write('\n已退出\n');
    process.exit();
  }

  switch (key.name) {
    case 's':
      if (running) {
        accumulated += Date.now() - startTime;
        running = false;
        stopTimer();
        updateDisplay();
      } else {
        startTime = Date.now();
        running = true;
        startTimer();
      }
      break;
    case 'r':
      stopTimer();
      accumulated = 0;
      startTime = Date.now();
      running = true;
      startTimer();
      updateDisplay();
      break;
    case 'q':
      stopTimer();
      process.stdout.write('\n已退出\n');
      process.exit();
      break;
  }
});
