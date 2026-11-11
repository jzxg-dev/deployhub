-- 服务器可以用平台保存的 SSH 私钥（凭据，种类 sshkey）登录：这里记凭据的名称，为空时用 key_path 指向的本机文件。
ALTER TABLE servers ADD COLUMN key_credential TEXT NOT NULL DEFAULT '';
