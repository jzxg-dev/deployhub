import { QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router";
import { Toaster } from "sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ApiError } from "@/lib/api";
import { router } from "./App";
import "./index.css";

const queryClient: QueryClient = new QueryClient({
  // 任何请求返回 401（登录过期、账号被停用）时重新确认登录状态，页面会回到登录页
  queryCache: new QueryCache({
    onError: (error, query) => {
      if (error instanceof ApiError && error.unauthorized && query.queryKey[0] !== "session") {
        void queryClient.invalidateQueries({ queryKey: ["session"] });
      }
    },
  }),
  defaultOptions: {
    queries: {
      // 未登录、接口不存在之类的错误重试没有意义
      retry: (count, error) => !(error instanceof ApiError && error.status >= 400 && error.status < 500) && count < 2,
      refetchOnWindowFocus: false,
    },
  },
});

// 跟随系统切换深色模式
window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", (e) => {
  document.documentElement.classList.toggle("dark", e.matches);
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <TooltipProvider delayDuration={200}>
        <RouterProvider router={router} />
        <Toaster position="top-center" richColors closeButton />
      </TooltipProvider>
    </QueryClientProvider>
  </StrictMode>,
);
