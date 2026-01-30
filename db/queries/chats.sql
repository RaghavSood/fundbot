-- name: GetChatByChatID :one
SELECT id, chat_id, title, created_at FROM chats WHERE chat_id = ?;

-- name: CreateChat :one
INSERT INTO chats (chat_id, title) VALUES (?, ?) RETURNING id, chat_id, title, created_at;

-- name: ListChats :many
SELECT id, chat_id, title, created_at FROM chats ORDER BY id;
