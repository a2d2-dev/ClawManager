import { useCallback, useEffect, useRef, useState } from "react";
import type { ClipboardEvent, KeyboardEvent } from "react";
import { useI18n } from "../contexts/I18nContext";

interface InstanceShellTerminalProps {
  instanceId: number;
  instanceName: string;
  isRunning: boolean;
  className?: string;
}

const MAX_TERMINAL_BUFFER = 80_000;
const ANSI_PATTERN =
  // eslint-disable-next-line no-control-regex
  /\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1B\\))/g;

const KEY_SEQUENCES: Record<string, string> = {
  ArrowUp: "\x1b[A",
  ArrowDown: "\x1b[B",
  ArrowRight: "\x1b[C",
  ArrowLeft: "\x1b[D",
  Home: "\x1b[H",
  End: "\x1b[F",
  Delete: "\x1b[3~",
  PageUp: "\x1b[5~",
  PageDown: "\x1b[6~",
};

export function InstanceShellTerminal({
  instanceId,
  instanceName,
  isRunning,
  className = "",
}: InstanceShellTerminalProps) {
  const { t } = useI18n();
  const [connected, setConnected] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [output, setOutput] = useState("");
  const socketRef = useRef<WebSocket | null>(null);
  const terminalRef = useRef<HTMLDivElement | null>(null);
  const outputRef = useRef<HTMLPreElement | null>(null);

  const appendOutput = useCallback((chunk: string) => {
    const normalized = chunk
      .replace(ANSI_PATTERN, "")
      .replace(/\r\n/g, "\n")
      .replace(/\r/g, "\n");
    setOutput((current) =>
      (current + normalized).slice(-MAX_TERMINAL_BUFFER),
    );
  }, []);

  const buildShellUrl = useCallback(() => {
    const token = localStorage.getItem("access_token");
    if (!token) {
      return null;
    }

    const explicitOrigin = import.meta.env.VITE_BACKEND_ORIGIN as
      | string
      | undefined;
    const base = explicitOrigin || window.location.origin;
    const url = new URL(`/api/v1/instances/${instanceId}/shell`, base);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    if (!explicitOrigin && window.location.port === "9002") {
      url.port = "9001";
    }
    url.searchParams.set("token", token);
    return url.toString();
  }, [instanceId]);

  const sendMessage = useCallback((data: string) => {
    const socket = socketRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      return;
    }
    socket.send(JSON.stringify({ type: "input", data }));
  }, []);

  const sendResize = useCallback(() => {
    const socket = socketRef.current;
    const terminal = terminalRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN || !terminal) {
      return;
    }

    const rect = terminal.getBoundingClientRect();
    const cols = Math.max(40, Math.floor(rect.width / 8));
    const rows = Math.max(12, Math.floor(rect.height / 18));
    socket.send(JSON.stringify({ type: "resize", cols, rows }));
  }, []);

  const disconnect = useCallback(() => {
    socketRef.current?.close();
    socketRef.current = null;
    setConnected(false);
    setConnecting(false);
  }, []);

  const connect = useCallback(() => {
    if (!isRunning || connecting || connected) {
      return;
    }

    const shellUrl = buildShellUrl();
    if (!shellUrl) {
      setError(t("instances.shellMissingToken"));
      return;
    }

    setConnecting(true);
    setError(null);
    setOutput("");

    const socket = new WebSocket(shellUrl);
    socketRef.current = socket;

    socket.onopen = () => {
      setConnected(true);
      setConnecting(false);
      appendOutput(t("instances.shellConnected", { name: instanceName }));
      window.setTimeout(sendResize, 0);
      terminalRef.current?.focus();
    };

    socket.onmessage = (event) => {
      if (typeof event.data === "string") {
        appendOutput(event.data);
        return;
      }

      if (event.data instanceof Blob) {
        void event.data.text().then(appendOutput);
      }
    };

    socket.onerror = () => {
      setError(t("instances.shellConnectionFailed"));
    };

    socket.onclose = () => {
      setConnected(false);
      setConnecting(false);
      appendOutput(t("instances.shellDisconnected"));
      if (socketRef.current === socket) {
        socketRef.current = null;
      }
    };
  }, [
    appendOutput,
    buildShellUrl,
    connected,
    connecting,
    instanceName,
    isRunning,
    sendResize,
    t,
  ]);

  useEffect(() => {
    return () => disconnect();
  }, [disconnect]);

  useEffect(() => {
    if (!isRunning) {
      disconnect();
    }
  }, [disconnect, isRunning]);

  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [output]);

  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal || typeof ResizeObserver === "undefined") {
      return;
    }

    const observer = new ResizeObserver(() => sendResize());
    observer.observe(terminal);
    return () => observer.disconnect();
  }, [sendResize]);

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!connected) {
      return;
    }

    if (event.ctrlKey && !event.altKey && !event.metaKey) {
      const key = event.key.toLowerCase();
      if (key === "c") {
        event.preventDefault();
        sendMessage("\x03");
        return;
      }
      if (key === "d") {
        event.preventDefault();
        sendMessage("\x04");
        return;
      }
      if (key === "l") {
        event.preventDefault();
        setOutput("");
        sendMessage("\x0c");
        return;
      }
    }

    if (event.key === "Enter") {
      event.preventDefault();
      sendMessage("\r");
      return;
    }
    if (event.key === "Backspace") {
      event.preventDefault();
      sendMessage("\x7f");
      return;
    }
    if (event.key === "Tab") {
      event.preventDefault();
      sendMessage("\t");
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      sendMessage("\x1b");
      return;
    }
    if (KEY_SEQUENCES[event.key]) {
      event.preventDefault();
      sendMessage(KEY_SEQUENCES[event.key]);
      return;
    }
    if (
      event.key.length === 1 &&
      !event.metaKey &&
      !event.altKey
    ) {
      event.preventDefault();
      sendMessage(event.key);
    }
  };

  const handlePaste = (event: ClipboardEvent<HTMLDivElement>) => {
    if (!connected) {
      return;
    }
    const text = event.clipboardData.getData("text");
    if (text) {
      event.preventDefault();
      sendMessage(text);
    }
  };

  if (!isRunning && !connected) {
    return (
      <div className={`app-panel border-dashed p-12 text-center ${className}`}>
        <h3 className="text-sm font-medium text-gray-900">
          {t("instances.startTheInstance")}
        </h3>
        <p className="mt-1 text-sm text-gray-500">
          {t("instances.startToAccessShell")}
        </p>
      </div>
    );
  }

  return (
    <div
      className={`relative flex min-h-[420px] flex-col overflow-hidden rounded-lg border border-[#1f2937] bg-[#0b1020] shadow-[0_30px_90px_-56px_rgba(17,24,39,0.9)] ${className}`}
    >
      <div className="flex items-center justify-between border-b border-[#202a3b] bg-[#111827] px-4 py-3 text-white">
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold">{instanceName}</p>
          <p className="mt-1 text-xs text-[#aab4c4]">
            {connected
              ? t("instances.shellConnectedStatus")
              : connecting
                ? t("instances.shellConnecting")
                : t("instances.shellReady")}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {connected ? (
            <button
              type="button"
              onClick={disconnect}
              className="rounded-md bg-[#243041] px-3 py-1.5 text-xs font-medium text-gray-200 hover:bg-[#31415a]"
            >
              {t("instances.disconnectShell")}
            </button>
          ) : (
            <button
              type="button"
              onClick={connect}
              disabled={connecting}
              className="rounded-md bg-indigo-500 px-3 py-1.5 text-xs font-medium text-white hover:bg-indigo-600 disabled:cursor-wait disabled:opacity-70"
            >
              {connecting
                ? t("instances.shellConnecting")
                : t("instances.connectShell")}
            </button>
          )}
        </div>
      </div>

      <div
        ref={terminalRef}
        tabIndex={0}
        onKeyDown={handleKeyDown}
        onPaste={handlePaste}
        className="min-h-0 flex-1 cursor-text outline-none"
      >
        <pre
          ref={outputRef}
          className="h-full overflow-auto whitespace-pre-wrap break-words px-4 py-4 font-mono text-[13px] leading-5 text-[#d7e1f2]"
        >
          {output ||
            (error
              ? error
              : connecting
                ? t("instances.shellConnecting")
                : t("instances.shellReady"))}
        </pre>
      </div>
    </div>
  );
}
