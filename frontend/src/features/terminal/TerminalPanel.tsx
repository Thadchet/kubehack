import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef, useState } from "react";
import { openSocket } from "../../api";
import type { DockTarget } from "../dock/Dock";

const fontFamily = '"Atkinson Hyperlegible Mono", ui-monospace, monospace';

type SessionStatus = "connecting" | "open" | "closed";

interface ExitMessage {
  type: "exit";
  code: number;
  message?: string;
}

function exitText({ code, message }: ExitMessage): string {
  // 126 to 128 are what runtimes report when the shell itself cannot start.
  if (code === -1 || (code >= 126 && code <= 128)) {
    return `Could not open a shell. This container may not have one. ${message ?? ""}`.trim();
  }
  return `Shell exited with code ${code}`;
}

function themeColors() {
  const styles = getComputedStyle(document.documentElement);
  const color = (name: string) => styles.getPropertyValue(name).trim();
  return {
    background: color("--ground"),
    foreground: color("--ink"),
    cursor: color("--ink"),
    selectionBackground: color("--raised"),
  };
}

interface SessionProps {
  target: DockTarget;
  container: string;
  active: boolean;
  onStatus: (status: SessionStatus, detail?: string) => void;
}

// Session owns one shell. Remounting it, by key, starts a new one.
function Session({ target, container, active, onStatus }: SessionProps) {
  const host = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal>(undefined);
  const fit = useRef<FitAddon>(undefined);
  const statusHandler = useRef(onStatus);
  statusHandler.current = onStatus;

  useEffect(() => {
    let stopped = false;
    let exited = false;
    let socket: WebSocket | undefined;
    const term = new Terminal({
      cursorBlink: true,
      fontFamily,
      fontSize: 12,
      scrollback: 5000,
      theme: themeColors(),
    });
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    terminal.current = term;
    fit.current = fitAddon;

    // The rest of the app follows the system through CSS, which xterm can't see.
    const scheme = window.matchMedia("(prefers-color-scheme: dark)");
    const applyTheme = () => {
      term.options.theme = themeColors();
    };
    scheme.addEventListener("change", applyTheme);
    // Switching cluster changes the hue on the root element.
    const hue = new MutationObserver(applyTheme);
    hue.observe(document.documentElement, { attributes: true, attributeFilter: ["style"] });

    const encoder = new TextEncoder();
    const sendInput = (text: string) => {
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(encoder.encode(text));
      }
    };
    term.onData(sendInput);
    term.onResize(({ cols, rows }) => {
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "resize", cols, rows }));
      }
    });

    const start = async () => {
      // xterm measures the font once, so it has to be loaded first.
      await document.fonts.load(`12px ${fontFamily}`);
      if (stopped) {
        return;
      }
      term.open(host.current!);
      fitAddon.fit();

      const query = new URLSearchParams({
        namespace: target.namespace,
        pod: target.pod,
        container,
        cols: String(term.cols),
        rows: String(term.rows),
      });
      const next = await openSocket(
        `/api/v1/contexts/${encodeURIComponent(target.context)}/exec?${query}`,
      );
      if (stopped) {
        next.close();
        return;
      }
      socket = next;
      next.binaryType = "arraybuffer";
      next.onopen = () => {
        statusHandler.current("open");
        term.focus();
      };
      next.onmessage = (event) => {
        if (typeof event.data === "string") {
          exited = true;
          statusHandler.current("closed", exitText(JSON.parse(event.data) as ExitMessage));
          return;
        }
        term.write(new Uint8Array(event.data as ArrayBuffer));
      };
      next.onclose = () => {
        // The socket also closes after an exit message, which says more.
        if (!stopped && !exited) {
          statusHandler.current("closed");
        }
      };
    };
    start().catch((error: Error) => statusHandler.current("closed", error.message));

    return () => {
      stopped = true;
      scheme.removeEventListener("change", applyTheme);
      hue.disconnect();
      socket?.close();
      term.dispose();
    };
    // target and container are fixed for a session's life, by the key.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const element = host.current!;
    const observer = new ResizeObserver(() => {
      // A hidden tab measures as zero, which would resize the shell to nothing.
      if (element.offsetWidth > 0 && element.offsetHeight > 0 && terminal.current?.element) {
        fit.current?.fit();
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (active) {
      terminal.current?.focus();
    }
  }, [active]);

  return <div className="terminal-host" ref={host} data-testid="terminal" />;
}

interface PanelProps {
  target: DockTarget;
  active: boolean;
}

// The panel stays mounted in the background so the shell and its scrollback
// survive switching tabs. Only closing the tab ends the session.
export function TerminalPanel({ target, active }: PanelProps) {
  const [container, setContainer] = useState(target.container);
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState<SessionStatus>("connecting");
  const [detail, setDetail] = useState<string>();

  const restart = () => {
    setStatus("connecting");
    setDetail(undefined);
    setAttempt((value) => value + 1);
  };

  return (
    <div className="log-panel" role="tabpanel" hidden={!active}>
      <div className="log-toolbar">
        <span className="muted">{target.context}</span>
        {target.containers.length > 1 ? (
          <select
            aria-label="Container"
            data-testid="terminal-container"
            value={container}
            onChange={(event) => {
              setContainer(event.target.value);
              restart();
            }}
          >
            {target.containers.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        ) : (
          <span>{container}</span>
        )}
        <span
          className="log-status wrap"
          data-testid="terminal-status"
          data-tone={status === "open" ? "good" : undefined}
        >
          {status === "open"
            ? "Connected"
            : status === "connecting"
              ? "Connecting"
              : (detail ?? "Disconnected")}
        </span>
        {status === "closed" && (
          <button type="button" onClick={restart}>
            Open a new shell
          </button>
        )}
      </div>
      <Session
        key={`${container}/${attempt}`}
        target={target}
        container={container}
        active={active}
        onStatus={(next, nextDetail) => {
          setStatus(next);
          setDetail(nextDetail);
        }}
      />
    </div>
  );
}
