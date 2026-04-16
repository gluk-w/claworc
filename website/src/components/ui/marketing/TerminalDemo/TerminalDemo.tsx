import { useEffect, useState, useRef } from 'react';

type LineType =
  | 'command'
  | 'intro'
  | 'prompt-label'
  | 'prompt-input'
  | 'prompt-hint'
  | 'select-label'
  | 'select-option'
  | 'select-option-active'
  | 'select-hint'
  | 'spinner'
  | 'success'
  | 'note-header'
  | 'note-content'
  | 'outro';

interface ScriptLine {
  text: string;
  delay: number;
  type: LineType;
}

// Astro Rocket setup flow: clone → install → dev
const script: ScriptLine[] = [
  // Step 1: clone
  { text: '$ git clone https://github.com/hansmartens68/Astro-Rocket.git', delay: 1200, type: 'command' },
  { text: '', delay: 200, type: 'command' },
  { text: 'Cloning into \'Astro-Rocket\'...', delay: 300, type: 'prompt-hint' },
  { text: '✔  Repository cloned', delay: 600, type: 'success' },
  { text: '', delay: 200, type: 'prompt-label' },

  // Step 2: install
  { text: '$ cd Astro-Rocket && pnpm install', delay: 600, type: 'command' },
  { text: '', delay: 200, type: 'command' },
  { text: '◐  Resolving dependencies...', delay: 400, type: 'spinner' },
  { text: '✔  Dependencies installed', delay: 700, type: 'success' },
  { text: '', delay: 200, type: 'prompt-label' },

  // Step 3: dev
  { text: '$ pnpm dev', delay: 600, type: 'command' },
  { text: '', delay: 200, type: 'command' },
  { text: '◐  Starting Astro dev server...', delay: 400, type: 'spinner' },
  { text: '✔  Astro dev server started', delay: 600, type: 'success' },
  { text: '│', delay: 100, type: 'prompt-label' },

  // Next steps note
  { text: '◇  Ready ──────────────────────────╮', delay: 300, type: 'note-header' },
  { text: '│                                  │', delay: 50, type: 'note-content' },
  { text: '│  localhost:4321                  │', delay: 100, type: 'note-content' },
  { text: '│                                  │', delay: 50, type: 'note-content' },
  { text: '├──────────────────────────────────╯', delay: 200, type: 'note-content' },
  { text: '│', delay: 100, type: 'prompt-label' },

  // Outro
  { text: '└  Happy building! 🚀', delay: 0, type: 'outro' },
];

export function TerminalDemo() {
  const [lines, setLines] = useState<{ text: string; type: LineType }[]>([]);
  const [_currentIndex, setCurrentIndex] = useState(0);
  const [typingText, setTypingText] = useState('');
  const [isTyping, setIsTyping] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const timeoutIds: ReturnType<typeof setTimeout>[] = [];

    const processLine = (index: number) => {
      if (index >= script.length) {
        // Restart after delay
        timeoutIds.push(
          setTimeout(() => {
            setLines([]);
            setCurrentIndex(0);
            setTypingText('');
            processLine(0);
          }, 6000)
        );
        return;
      }

      const line = script[index];

      // For command and input lines, type character by character
      if (line.type === 'command' || line.type === 'prompt-input') {
        setIsTyping(true);
        let charIdx = 0;

        const typeChar = () => {
          if (charIdx <= line.text.length) {
            setTypingText(line.text.slice(0, charIdx));
            charIdx++;
            timeoutIds.push(setTimeout(typeChar, 35));
          } else {
            setIsTyping(false);
            setLines(prev => [...prev, { text: line.text, type: line.type }]);
            setTypingText('');
            setCurrentIndex(index + 1);
            timeoutIds.push(setTimeout(() => processLine(index + 1), line.delay));
          }
        };
        typeChar();
      } else if (line.type === 'spinner') {
        // Show spinner briefly then replace with success
        setLines(prev => [...prev, { text: line.text, type: line.type }]);
        setCurrentIndex(index + 1);
        timeoutIds.push(setTimeout(() => {
          // Remove spinner line and add success
          setLines(prev => prev.slice(0, -1));
          processLine(index + 1);
        }, line.delay));
      } else {
        // Instant display for other lines
        setLines(prev => [...prev, { text: line.text, type: line.type }]);
        setCurrentIndex(index + 1);
        timeoutIds.push(setTimeout(() => processLine(index + 1), line.delay));
      }
    };

    processLine(0);

    return () => {
      timeoutIds.forEach(clearTimeout);
    };
  }, []);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [lines, typingText]);

  const getLineClass = (type: LineType): string => {
    switch (type) {
      case 'command':
        return 'text-on-invert';
      case 'intro':
        return 'text-cyan-400 font-bold';
      case 'prompt-label':
        return 'text-gray-300';
      case 'prompt-input':
        return 'text-on-invert';
      case 'prompt-hint':
        return 'text-gray-300';
      case 'select-label':
        return 'text-gray-300';
      case 'select-option':
        return 'text-gray-300';
      case 'select-option-active':
        return 'text-cyan-400';
      case 'select-hint':
        return 'text-gray-300';
      case 'spinner':
        return 'text-cyan-400 animate-pulse';
      case 'success':
        return 'text-green-400';
      case 'note-header':
        return 'text-gray-300';
      case 'note-content':
        return 'text-gray-300';
      case 'outro':
        return 'text-green-400 font-bold';
      default:
        return 'text-on-invert';
    }
  };

  return (
    <div className="w-full max-w-lg overflow-hidden rounded-md border border-border-invert bg-surface-invert shadow-xl mx-auto lg:mx-0">
      {/* Window Chrome */}
      <div className="flex items-center gap-2 border-b border-border-invert bg-surface-invert-secondary px-4 py-3">
        <div className="h-3 w-3 rounded-full bg-[#FF5F56]" />
        <div className="h-3 w-3 rounded-full bg-[#FFBD2E]" />
        <div className="h-3 w-3 rounded-full bg-[#27C93F]" />
        <span className="ml-2 text-xs font-mono text-on-invert-muted">terminal</span>
      </div>

      {/* Terminal Content */}
      <div
        ref={scrollRef}
        className="h-[380px] overflow-y-auto p-4 font-mono text-xs leading-5"
      >
        {lines.map((line, idx) => (
          <div key={idx} className={getLineClass(line.type)}>
            {line.text}
          </div>
        ))}
        {isTyping && (
          <div className="text-on-invert">
            {typingText}
            <span className="inline-block h-4 w-2 bg-on-invert animate-pulse align-middle ml-0.5" />
          </div>
        )}
      </div>
    </div>
  );
}

export default TerminalDemo;
