-- 访问令牌可以在页面上再次查看：令牌本身用主密钥加密后保存（认证仍然按 token_hash 查）。
-- 这次升级之前创建的令牌库里只有哈希，这一列是空的，查看不了。
ALTER TABLE access_tokens ADD COLUMN token_enc TEXT NOT NULL DEFAULT '';
