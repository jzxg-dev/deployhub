import { useEffect, useState } from "react";

/** 值停止变化 delay 毫秒后才更新，用于输入时的识别和预览请求。 */
export function useDebounced<T>(value: T, delay: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), delay);
    return () => clearTimeout(t);
  }, [value, delay]);
  return v;
}
