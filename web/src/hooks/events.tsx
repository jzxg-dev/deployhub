import { useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import type { Job } from "@/lib/types";

// 整个页面共用一条 SSE 连接（/api/events）。各页面用 useServerEvent 订阅自己关心的事件。

export type StreamStatus = "connecting" | "open" | "closed";

type Listener = (data: unknown) => void;

interface EventStreamState {
  status: StreamStatus;
  lastEventAt?: Date;
  subscribe: (type: string, fn: Listener) => () => void;
}

const EventStreamContext = createContext<EventStreamState>({ status: "connecting", subscribe: () => () => {} });

export function EventStreamProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<{ status: StreamStatus; lastEventAt?: Date }>({ status: "connecting" });
  const listeners = useRef(new Map<string, Set<Listener>>());
  const qc = useQueryClient();

  const subscribe = useCallback((type: string, fn: Listener) => {
    const set = listeners.current.get(type) ?? new Set();
    set.add(fn);
    listeners.current.set(type, set);
    return () => {
      set.delete(fn);
    };
  }, []);

  // 标签页在后台时断开连接：浏览器对同一地址最多同时 6 个连接，多开几个标签页就会把普通请求堵住。
  // 回到前台时重新连接，并刷新数据补上后台期间的变化。
  useEffect(() => {
    let es: EventSource | null = null;
    const touch = () => setState({ status: "open", lastEventAt: new Date() });
    const dispatch = (type: string) => (ev: MessageEvent) => {
      touch();
      let data: unknown;
      try {
        data = (JSON.parse(ev.data) as { data?: unknown }).data;
      } catch {
        return;
      }
      listeners.current.get(type)?.forEach((fn) => fn(data));
    };
    const open = () => {
      if (es) return;
      const src = new EventSource("/api/events");
      es = src;
      src.addEventListener("hello", touch);
      src.addEventListener("ping", touch);
      src.addEventListener("job_event", dispatch("job_event"));
      src.addEventListener("job", dispatch("job"));
      src.onopen = () => setState((s) => ({ ...s, status: "open" }));
      // 浏览器会自动重连；readyState 为 CLOSED 时才是彻底断开（如服务返回 401）
      src.onerror = () => setState((s) => ({ ...s, status: src.readyState === EventSource.CLOSED ? "closed" : "connecting" }));
    };
    const close = () => {
      es?.close();
      es = null;
      setState((s) => ({ ...s, status: "connecting" }));
    };
    const onVisibility = () => {
      if (document.visibilityState === "visible") {
        open();
        void qc.invalidateQueries();
      } else {
        close();
      }
    };
    if (document.visibilityState === "visible") open();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      es?.close();
    };
  }, [qc]);

  // 任务状态变化：更新任务详情缓存；任务结束时刷新列表和线上状态
  useEffect(
    () =>
      subscribe("job", (data) => {
        const job = data as Job;
        qc.setQueryData(["jobs", job.id], job);
        void qc.invalidateQueries({ queryKey: ["jobs", "list"] });
        if (job.status !== "running" && job.status !== "queued") {
          for (const u of job.units) void qc.invalidateQueries({ queryKey: ["state", u.project, u.app] });
          void qc.invalidateQueries({ queryKey: ["status"] });
        }
      }),
    [subscribe, qc],
  );

  return <EventStreamContext.Provider value={{ ...state, subscribe }}>{children}</EventStreamContext.Provider>;
}

export function useEventStream() {
  return useContext(EventStreamContext);
}

/** 订阅一种服务器事件；handler 可以每次渲染都不同，不会重复订阅。 */
export function useServerEvent<T>(type: string, handler: (data: T) => void) {
  const { subscribe } = useEventStream();
  const ref = useRef(handler);
  ref.current = handler;
  useEffect(() => subscribe(type, (d) => ref.current(d as T)), [subscribe, type]);
}
