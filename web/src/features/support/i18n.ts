/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import i18n from '@/i18n/config'

// Translations for the Support page, in every language the console offers.
//
// They are registered at runtime instead of being written into
// src/i18n/locales/*.json on purpose: those files are on the never-touch
// list (AGENT-WORKFLOW.md §3-4) because they carry the licence-required
// attribution key, and a JSON round-trip has already destroyed it once.
// Keys are the English text, as everywhere else in the console, so English
// needs no table. `overwrite: false` means an existing upstream translation
// of a shared key (e.g. "Close") wins over ours.

export const SUPPORT_KEYS = [
  'Support',
  'Support inbox',
  'Questions, problems or feedback — write to us here.',
  'Customer questions. Every new customer message is also emailed to staff.',
  'Waiting for us',
  'Replied',
  'Closed',
  'Needs reply',
  'Waiting on customer',
  'All',
  'Unify API support',
  'Customer',
  'You',
  'Ask a question',
  'Subject, e.g. "How do I raise my rate limit?"',
  'Tell us what you need. Include the model, the request id or the error text if there is one.',
  'We reply here and email you when we do.',
  'Send',
  'Sending…',
  'Loading…',
  'Not found',
  'Your conversations',
  'No questions yet. Ask one and we will get back to you.',
  '← All conversations',
  '← Inbox',
  'Add a message',
  'Write to reopen this conversation',
  'Mark as resolved',
  'Reply to the customer — they get it by email too',
  'Send reply',
  'Close',
  'Reopen',
  'Nothing here.',
  'Subject',
  'Last activity',
  'Status',
  'user #{{id}}',
  // Server messages a customer can hit (controller/unifyapi_support.go).
  'could not save your message, please try again',
  'you have sent a lot of messages in the last hour; please wait a little',
  'you have too many open conversations; add to one of them instead',
  'conversation not found',
] as const

type Key = (typeof SUPPORT_KEYS)[number]
type Table = Record<Key, string>

const zhCN: Table = {
  Support: '客服支持',
  'Support inbox': '客服收件箱',
  'Questions, problems or feedback — write to us here.':
    '有问题、遇到故障或想提建议？在这里给我们留言。',
  'Customer questions. Every new customer message is also emailed to staff.':
    '客户提问。每条新的客户消息也会通过邮件发送给管理员。',
  'Waiting for us': '等待回复',
  Replied: '已回复',
  Closed: '已关闭',
  'Needs reply': '待回复',
  'Waiting on customer': '等客户回复',
  All: '全部',
  'Unify API support': 'Unify API 客服',
  Customer: '客户',
  You: '你',
  'Ask a question': '提一个问题',
  'Subject, e.g. "How do I raise my rate limit?"':
    '标题，例如“如何提高我的速率限制？”',
  'Tell us what you need. Include the model, the request id or the error text if there is one.':
    '请描述你的需求。如有相关的模型、请求 ID 或错误信息，请一并附上。',
  'We reply here and email you when we do.':
    '我们会在这里回复，并同时发邮件通知你。',
  Send: '发送',
  'Sending…': '发送中…',
  'Loading…': '加载中…',
  'Not found': '未找到',
  'Your conversations': '你的对话',
  'No questions yet. Ask one and we will get back to you.':
    '还没有提问。提一个问题，我们会尽快回复你。',
  '← All conversations': '← 全部对话',
  '← Inbox': '← 收件箱',
  'Add a message': '继续留言',
  'Write to reopen this conversation': '留言即可重新打开此对话',
  'Mark as resolved': '标记为已解决',
  'Reply to the customer — they get it by email too':
    '回复客户（客户也会收到邮件）',
  'Send reply': '发送回复',
  Close: '关闭',
  Reopen: '重新打开',
  'Nothing here.': '暂无内容。',
  Subject: '标题',
  'Last activity': '最近活动',
  Status: '状态',
  'user #{{id}}': '用户 #{{id}}',
  'could not save your message, please try again': '消息保存失败，请重试',
  'you have sent a lot of messages in the last hour; please wait a little':
    '你在过去一小时内发送了太多消息，请稍后再试',
  'you have too many open conversations; add to one of them instead':
    '你未关闭的对话太多了，请在已有对话中继续留言',
  'conversation not found': '未找到该对话',
}

const zhTW: Table = {
  Support: '客服支援',
  'Support inbox': '客服收件匣',
  'Questions, problems or feedback — write to us here.':
    '有問題、遇到故障或想提建議？在這裡留言給我們。',
  'Customer questions. Every new customer message is also emailed to staff.':
    '客戶提問。每則新的客戶訊息也會以電子郵件寄給管理員。',
  'Waiting for us': '等待回覆',
  Replied: '已回覆',
  Closed: '已關閉',
  'Needs reply': '待回覆',
  'Waiting on customer': '等客戶回覆',
  All: '全部',
  'Unify API support': 'Unify API 客服',
  Customer: '客戶',
  You: '你',
  'Ask a question': '提出問題',
  'Subject, e.g. "How do I raise my rate limit?"':
    '標題，例如「如何提高我的速率限制？」',
  'Tell us what you need. Include the model, the request id or the error text if there is one.':
    '請描述你的需求。如有相關的模型、請求 ID 或錯誤訊息，請一併附上。',
  'We reply here and email you when we do.':
    '我們會在這裡回覆，並同時寄送電子郵件通知你。',
  Send: '傳送',
  'Sending…': '傳送中…',
  'Loading…': '載入中…',
  'Not found': '找不到',
  'Your conversations': '你的對話',
  'No questions yet. Ask one and we will get back to you.':
    '還沒有提問。提出問題，我們會儘快回覆你。',
  '← All conversations': '← 全部對話',
  '← Inbox': '← 收件匣',
  'Add a message': '繼續留言',
  'Write to reopen this conversation': '留言即可重新開啟此對話',
  'Mark as resolved': '標記為已解決',
  'Reply to the customer — they get it by email too':
    '回覆客戶（客戶也會收到電子郵件）',
  'Send reply': '傳送回覆',
  Close: '關閉',
  Reopen: '重新開啟',
  'Nothing here.': '目前沒有內容。',
  Subject: '標題',
  'Last activity': '最近活動',
  Status: '狀態',
  'user #{{id}}': '使用者 #{{id}}',
  'could not save your message, please try again': '訊息儲存失敗，請重試',
  'you have sent a lot of messages in the last hour; please wait a little':
    '你在過去一小時內傳送了太多訊息，請稍後再試',
  'you have too many open conversations; add to one of them instead':
    '你未關閉的對話太多了，請在既有對話中繼續留言',
  'conversation not found': '找不到此對話',
}

const fr: Table = {
  Support: 'Support',
  'Support inbox': 'Boîte de réception du support',
  'Questions, problems or feedback — write to us here.':
    'Une question, un problème ou une suggestion ? Écrivez-nous ici.',
  'Customer questions. Every new customer message is also emailed to staff.':
    'Questions des clients. Chaque nouveau message client est aussi envoyé par e-mail à l’équipe.',
  'Waiting for us': 'En attente de notre réponse',
  Replied: 'Répondu',
  Closed: 'Fermé',
  'Needs reply': 'À traiter',
  'Waiting on customer': 'En attente du client',
  All: 'Tous',
  'Unify API support': 'Support Unify API',
  Customer: 'Client',
  You: 'Vous',
  'Ask a question': 'Poser une question',
  'Subject, e.g. "How do I raise my rate limit?"':
    'Objet, p. ex. « Comment augmenter ma limite de débit ? »',
  'Tell us what you need. Include the model, the request id or the error text if there is one.':
    'Décrivez votre besoin. Indiquez le modèle, l’identifiant de requête ou le message d’erreur le cas échéant.',
  'We reply here and email you when we do.':
    'Nous répondons ici et vous prévenons par e-mail.',
  Send: 'Envoyer',
  'Sending…': 'Envoi…',
  'Loading…': 'Chargement…',
  'Not found': 'Introuvable',
  'Your conversations': 'Vos conversations',
  'No questions yet. Ask one and we will get back to you.':
    'Aucune question pour l’instant. Posez-en une et nous vous répondrons.',
  '← All conversations': '← Toutes les conversations',
  '← Inbox': '← Boîte de réception',
  'Add a message': 'Ajouter un message',
  'Write to reopen this conversation':
    'Écrivez pour rouvrir cette conversation',
  'Mark as resolved': 'Marquer comme résolu',
  'Reply to the customer — they get it by email too':
    'Répondre au client — il la reçoit aussi par e-mail',
  'Send reply': 'Envoyer la réponse',
  Close: 'Fermer',
  Reopen: 'Rouvrir',
  'Nothing here.': 'Rien ici.',
  Subject: 'Objet',
  'Last activity': 'Dernière activité',
  Status: 'Statut',
  'user #{{id}}': 'utilisateur n° {{id}}',
  'could not save your message, please try again':
    'Impossible d’enregistrer votre message, veuillez réessayer',
  'you have sent a lot of messages in the last hour; please wait a little':
    'Vous avez envoyé beaucoup de messages au cours de la dernière heure ; veuillez patienter un peu',
  'you have too many open conversations; add to one of them instead':
    'Vous avez trop de conversations ouvertes ; ajoutez plutôt un message à l’une d’elles',
  'conversation not found': 'Conversation introuvable',
}

const ja: Table = {
  Support: 'サポート',
  'Support inbox': 'サポート受信箱',
  'Questions, problems or feedback — write to us here.':
    'ご質問・不具合・ご意見は、こちらからお送りください。',
  'Customer questions. Every new customer message is also emailed to staff.':
    'お客様からの質問です。新しいメッセージはすべてスタッフにもメールで通知されます。',
  'Waiting for us': '回答待ち',
  Replied: '回答済み',
  Closed: 'クローズ',
  'Needs reply': '要返信',
  'Waiting on customer': 'お客様の返信待ち',
  All: 'すべて',
  'Unify API support': 'Unify API サポート',
  Customer: 'お客様',
  You: 'あなた',
  'Ask a question': '質問する',
  'Subject, e.g. "How do I raise my rate limit?"':
    '件名（例：「レート制限を上げるには？」）',
  'Tell us what you need. Include the model, the request id or the error text if there is one.':
    'ご用件をお書きください。モデル名、リクエスト ID、エラーメッセージがあれば併せてお知らせください。',
  'We reply here and email you when we do.':
    'こちらで回答し、メールでもお知らせします。',
  Send: '送信',
  'Sending…': '送信中…',
  'Loading…': '読み込み中…',
  'Not found': '見つかりません',
  'Your conversations': 'あなたの問い合わせ',
  'No questions yet. Ask one and we will get back to you.':
    'まだ質問はありません。質問をお送りいただければ、折り返しご連絡します。',
  '← All conversations': '← すべての問い合わせ',
  '← Inbox': '← 受信箱',
  'Add a message': 'メッセージを追加',
  'Write to reopen this conversation': 'メッセージを送ると再オープンされます',
  'Mark as resolved': '解決済みにする',
  'Reply to the customer — they get it by email too':
    'お客様に返信（メールでも届きます）',
  'Send reply': '返信を送信',
  Close: 'クローズ',
  Reopen: '再オープン',
  'Nothing here.': '項目はありません。',
  Subject: '件名',
  'Last activity': '最終更新',
  Status: 'ステータス',
  'user #{{id}}': 'ユーザー #{{id}}',
  'could not save your message, please try again':
    'メッセージを保存できませんでした。もう一度お試しください',
  'you have sent a lot of messages in the last hour; please wait a little':
    '直近1時間に送信したメッセージが多すぎます。しばらくお待ちください',
  'you have too many open conversations; add to one of them instead':
    '未解決の問い合わせが多すぎます。既存の問い合わせにメッセージを追加してください',
  'conversation not found': '問い合わせが見つかりません',
}

const ru: Table = {
  Support: 'Поддержка',
  'Support inbox': 'Входящие поддержки',
  'Questions, problems or feedback — write to us here.':
    'Вопросы, проблемы или пожелания — напишите нам здесь.',
  'Customer questions. Every new customer message is also emailed to staff.':
    'Вопросы клиентов. Каждое новое сообщение клиента также отправляется сотрудникам по почте.',
  'Waiting for us': 'Ожидает нашего ответа',
  Replied: 'Отвечено',
  Closed: 'Закрыто',
  'Needs reply': 'Требует ответа',
  'Waiting on customer': 'Ожидает клиента',
  All: 'Все',
  'Unify API support': 'Поддержка Unify API',
  Customer: 'Клиент',
  You: 'Вы',
  'Ask a question': 'Задать вопрос',
  'Subject, e.g. "How do I raise my rate limit?"':
    'Тема, например «Как повысить лимит запросов?»',
  'Tell us what you need. Include the model, the request id or the error text if there is one.':
    'Опишите, что вам нужно. Укажите модель, ID запроса или текст ошибки, если они есть.',
  'We reply here and email you when we do.':
    'Мы ответим здесь и сообщим вам по почте.',
  Send: 'Отправить',
  'Sending…': 'Отправка…',
  'Loading…': 'Загрузка…',
  'Not found': 'Не найдено',
  'Your conversations': 'Ваши обращения',
  'No questions yet. Ask one and we will get back to you.':
    'Вопросов пока нет. Задайте вопрос, и мы ответим.',
  '← All conversations': '← Все обращения',
  '← Inbox': '← Входящие',
  'Add a message': 'Добавить сообщение',
  'Write to reopen this conversation':
    'Напишите сообщение, чтобы снова открыть обращение',
  'Mark as resolved': 'Отметить как решённое',
  'Reply to the customer — they get it by email too':
    'Ответ клиенту — он также получит его по почте',
  'Send reply': 'Отправить ответ',
  Close: 'Закрыть',
  Reopen: 'Открыть снова',
  'Nothing here.': 'Здесь пусто.',
  Subject: 'Тема',
  'Last activity': 'Последняя активность',
  Status: 'Статус',
  'user #{{id}}': 'пользователь №{{id}}',
  'could not save your message, please try again':
    'Не удалось сохранить сообщение, попробуйте ещё раз',
  'you have sent a lot of messages in the last hour; please wait a little':
    'За последний час вы отправили слишком много сообщений; подождите немного',
  'you have too many open conversations; add to one of them instead':
    'У вас слишком много открытых обращений; добавьте сообщение в одно из них',
  'conversation not found': 'Обращение не найдено',
}

const vi: Table = {
  Support: 'Hỗ trợ',
  'Support inbox': 'Hộp thư hỗ trợ',
  'Questions, problems or feedback — write to us here.':
    'Câu hỏi, sự cố hay góp ý — hãy viết cho chúng tôi tại đây.',
  'Customer questions. Every new customer message is also emailed to staff.':
    'Câu hỏi của khách hàng. Mỗi tin nhắn mới của khách hàng cũng được gửi email cho nhân viên.',
  'Waiting for us': 'Chờ chúng tôi trả lời',
  Replied: 'Đã trả lời',
  Closed: 'Đã đóng',
  'Needs reply': 'Cần trả lời',
  'Waiting on customer': 'Chờ khách hàng',
  All: 'Tất cả',
  'Unify API support': 'Hỗ trợ Unify API',
  Customer: 'Khách hàng',
  You: 'Bạn',
  'Ask a question': 'Đặt câu hỏi',
  'Subject, e.g. "How do I raise my rate limit?"':
    'Tiêu đề, ví dụ: "Làm sao để tăng giới hạn tốc độ?"',
  'Tell us what you need. Include the model, the request id or the error text if there is one.':
    'Cho chúng tôi biết bạn cần gì. Hãy ghi kèm mô hình, mã yêu cầu hoặc nội dung lỗi nếu có.',
  'We reply here and email you when we do.':
    'Chúng tôi sẽ trả lời tại đây và gửi email báo cho bạn.',
  Send: 'Gửi',
  'Sending…': 'Đang gửi…',
  'Loading…': 'Đang tải…',
  'Not found': 'Không tìm thấy',
  'Your conversations': 'Các cuộc trò chuyện của bạn',
  'No questions yet. Ask one and we will get back to you.':
    'Chưa có câu hỏi nào. Hãy đặt câu hỏi, chúng tôi sẽ phản hồi bạn.',
  '← All conversations': '← Tất cả cuộc trò chuyện',
  '← Inbox': '← Hộp thư',
  'Add a message': 'Thêm tin nhắn',
  'Write to reopen this conversation':
    'Viết tin nhắn để mở lại cuộc trò chuyện',
  'Mark as resolved': 'Đánh dấu đã giải quyết',
  'Reply to the customer — they get it by email too':
    'Trả lời khách hàng — họ cũng nhận được qua email',
  'Send reply': 'Gửi trả lời',
  Close: 'Đóng',
  Reopen: 'Mở lại',
  'Nothing here.': 'Không có gì ở đây.',
  Subject: 'Tiêu đề',
  'Last activity': 'Hoạt động gần nhất',
  Status: 'Trạng thái',
  'user #{{id}}': 'người dùng #{{id}}',
  'could not save your message, please try again':
    'Không thể lưu tin nhắn, vui lòng thử lại',
  'you have sent a lot of messages in the last hour; please wait a little':
    'Bạn đã gửi quá nhiều tin nhắn trong một giờ qua; vui lòng đợi một chút',
  'you have too many open conversations; add to one of them instead':
    'Bạn có quá nhiều cuộc trò chuyện đang mở; hãy thêm tin nhắn vào một cuộc trò chuyện có sẵn',
  'conversation not found': 'Không tìm thấy cuộc trò chuyện',
}

// Language codes as the console's i18n config names them.
export const SUPPORT_TRANSLATIONS: Record<string, Table> = {
  zhCN,
  zhTW,
  fr,
  ja,
  ru,
  vi,
}

for (const [lng, table] of Object.entries(SUPPORT_TRANSLATIONS)) {
  i18n.addResourceBundle(lng, 'translation', table, true, false)
}
