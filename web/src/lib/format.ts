/** 时间显示：今天只显示时分，今年显示月日时分，否则显示完整日期。 */
export function formatTime(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  const now = new Date();
  const hm = d.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
  if (d.toDateString() === now.toDateString()) return `今天 ${hm}`;
  if (d.getFullYear() === now.getFullYear()) return `${d.getMonth() + 1}月${d.getDate()}日 ${hm}`;
  return d.toLocaleDateString("zh-CN") + " " + hm;
}

/** 相对时间，如「3 分钟前」。 */
export function timeAgo(iso?: string): string {
  if (!iso) return "—";
  const sec = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return "刚刚";
  if (sec < 3600) return `${Math.floor(sec / 60)} 分钟前`;
  if (sec < 86400) return `${Math.floor(sec / 3600)} 小时前`;
  if (sec < 86400 * 30) return `${Math.floor(sec / 86400)} 天前`;
  return formatTime(iso);
}
