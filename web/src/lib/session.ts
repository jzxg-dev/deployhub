import { http } from "@/lib/api";

/**
 * 退出登录。之后整页重新加载，而不是只清登录状态：
 * 内存里缓存的上一个账号的数据要全部丢掉，同一个浏览器换账号登录时不能看到。
 */
export async function logout() {
  try {
    await http.post("/api/logout");
  } finally {
    window.location.assign("/");
  }
}
