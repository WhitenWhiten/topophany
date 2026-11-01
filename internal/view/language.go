package view

import (
	"fmt"
	"strings"
)

// Text translates UI-owned words only. State codes, IDs and evidence payloads
// remain unchanged. Language belongs to each view, never to process-global state.
func Text(language, text string) string {
	pair, ok := uiWords[text]
	if !ok {
		return text
	}
	switch language {
	case "zh":
		return pair[0]
	case "ja":
		return pair[1]
	}
	return text
}

var uiWords = map[string][2]string{
	"STATE":           {"状态", "状態"},
	"SERVICE":         {"服务", "サービス"},
	"HOST":            {"主机", "ホスト"},
	"ENDPOINTS":       {"端点", "エンドポイント"},
	"OBSERVER":        {"观测点", "観測点"},
	"LAST SEEN":       {"最近联系", "最終応答"},
	"Needs a look":    {"需要处理", "要確認"},
	"Coverage":        {"覆盖说明", "監視範囲"},
	"nothing":         {"无", "なし"},
	"live":            {"在线", "オンライン"},
	"none":            {"无", "なし"},
	"DECLARED TARGET": {"声明目标", "宣言された対象"},
	"INCIDENTS":       {"事件", "インシデント"},

	"warning":       {"警告", "警告"},
	"all observers": {"所有观测点", "すべての観測点"}, "LIVE": {"实时", "ライブ"}, "PREVIEW": {"演示", "プレビュー"}, "Project": {"项目", "プロジェクト"}, "Collector": {"采集器", "コレクター"}, "Updated": {"更新时间", "更新時刻"},
	"No collector answered": {"没有采集器响应", "コレクターから応答がありません"}, "The collector refused this client's token": {"采集器拒绝了此查看端的令牌", "閲覧用トークンが拒否されました"}, "The collector serves another project": {"采集器属于另一个项目", "コレクターのプロジェクトが異なります"}, "Could not read from the collector": {"无法读取采集器数据", "コレクターを読み取れません"}, "Save a valid viewer token with topo login.": {"请使用 topo login 保存有效的 Viewer 令牌。", "topo login で有効な閲覧用トークンを保存してください。"}, "Check the project given to topo login.": {"请检查 topo login 中的项目 ID。", "topo login のプロジェクト ID を確認してください。"}, "Check the address with topo login, or see the whole UI on synthetic evidence with topo preview.": {"使用 topo login 检查连接地址，或运行 topo preview 体验模拟界面。", "topo login でアドレスを確認するか、topo preview でデモを試してください。"}, "Starting the preview": {"正在启动演示", "プレビューを開始中"}, "Connecting to the collector": {"正在连接采集器", "コレクターに接続中"}, "Connecting to ": {"正在连接 ", "接続中："}, "Trying again…": {"正在重试…", "再試行中…"},
	"Overview": {"概览", "概要"}, "Topology": {"拓扑", "トポロジー"}, "Servers": {"服务器", "サーバー"}, "Services": {"服务", "サービス"}, "Paths": {"路径", "経路"}, "Checks": {"检查", "チェック"}, "Incidents": {"事件", "インシデント"}, "Observers": {"观测点", "観測点"}, "About": {"关于", "情報"},
	"STATUS": {"状态", "状態"}, "NEEDS ATTENTION": {"需要处理", "要確認"}, "NETWORK": {"网络", "ネットワーク"}, "SERVICES": {"服务", "サービス"}, "OBSERVERS": {"观测点", "観測点"},
	"Details": {"详情", "詳細"}, "Filter": {"筛选", "絞り込み"}, "Related": {"关联事件", "関連項目"}, "View": {"视角", "観測点"}, "Refresh": {"刷新", "更新"}, "Scenario": {"场景", "シナリオ"}, "Map": {"地图", "地図"}, "Zoom": {"缩放", "ズーム"}, "Panels": {"面板", "パネル"}, "Full map": {"全屏地图", "全画面地図"}, "Settings": {"设置", "設定"}, "Quit": {"退出", "終了"}, "Save": {"保存", "保存"}, "Undo": {"撤销", "取り消す"}, "Add": {"添加", "追加"}, "Edit": {"编辑", "編集"}, "Delete": {"删除", "削除"},
	"ALL CLEAR": {"当前检查全部通过", "現在のチェックは正常"}, "LOCAL DRAFT": {"本地草稿", "ローカル下書き"}, "CACHED": {"缓存数据", "キャッシュ"}, "NO CHECKS": {"未配置检查", "チェックなし"}, "DEGRADED": {"存在异常", "一部異常"}, "NO EVIDENCE": {"暂无证据", "証拠なし"}, "INCOMPLETE EVIDENCE": {"证据不完整", "証拠が不完全"}, "CONNECTING": {"正在连接", "接続中"}, "LOADING": {"正在加载", "読み込み中"}, "OFFLINE": {"无法连接", "接続できません"},
	"✓ Nothing needs attention": {"✓ 当前检查没有异常", "✓ 現在のチェックに異常なし"}, "Every active check in this view passes; no incidents.": {"此视角的活跃检查均通过；没有事件。", "この観測点の有効なチェックはすべて成功し、インシデントはありません。"},
	"◌ Status not established": {"◌ 当前状态无法确认", "◌ 現在の状態は未確認"}, "Showing the last snapshot; nothing current is known.": {"正在显示上次快照，不能据此判断当前状态。", "前回のスナップショットです。現在の状態は判断できません。"},
	"○ Evidence incomplete": {"○ 证据不完整", "○ 証拠が不完全"}, "Some active checks have no current conclusive evidence.": {"部分活跃检查缺少当前有效证据。", "一部の有効なチェックに現在の確かな証拠がありません。"},
	"○ No checks in this view": {"○ 此视角没有检查", "○ この観測点にチェックなし"}, "Nothing is measured from here yet.": {"尚未从此处进行测量。", "この観測点ではまだ測定していません。"},
	"checks passing": {"检查通过", "チェック成功"}, "checks failing": {"检查失败", "チェック失敗"}, "checks warning": {"检查警告", "チェック警告"}, "checks not current": {"检查证据已过期", "期限切れのチェック"}, "checks without current evidence": {"检查缺少有效证据", "現在の証拠がないチェック"}, "up": {"正常", "正常"}, "warn": {"警告", "警告"}, "down": {"故障", "障害"}, "unknown": {"未知", "不明"},
	"LOADING RESOURCE DATA": {"正在读取资源", "リソース読込中"}, "RESOURCE DATA CURRENT": {"资源读数有效", "リソース測定値は有効"}, "RESOURCE DATA PARTIAL": {"部分资源读数缺失", "一部のリソース測定値が不足"}, "NO RESOURCE DATA": {"暂无资源读数", "リソース測定値なし"}, "RESOURCE SAMPLES EXPIRED": {"资源样本已过期", "リソース測定値の期限切れ"}, "RESOURCE DATA UNAVAILABLE": {"无法读取资源", "リソースを取得できません"}, "Resource telemetry does not establish application or network reachability.": {"资源读数不能证明应用健康或网络可达。", "リソース測定値だけではアプリの正常性やネットワーク到達性を判断できません。"},
	"No check is configured for this service.": {"此服务尚未配置检查。", "このサービスにチェックがありません。"}, "All configured checks are paused or in maintenance.": {"已配置的检查均已暂停或处于维护状态。", "すべてのチェックが一時停止またはメンテナンス中です。"}, "This observer has no active check for this service.": {"此观测点没有针对该服务的活跃检查。", "この観測点にはサービスの有効なチェックがありません。"}, "Configured checks do not establish application availability. Add an HTTP application check.": {"现有检查不能确认应用可用性；请补充 HTTP 应用检查。", "現在のチェックではアプリの可用性は確認できません。HTTP アプリチェックを追加してください。"}, "Application evidence has expired. Check the worker and its delivery.": {"应用证据已过期，请检查 worker 及投递状态。", "アプリの証拠が期限切れです。ワーカーと配送状態を確認してください。"}, "Waiting for the first application sample in this view.": {"正在等待此视角的首个应用样本。", "この観測点の最初のアプリ測定値を待っています。"}, "Current checks do not establish application availability; inspect path and monitor-side evidence.": {"当前检查无法确认应用可用性，请检查路径及监控端证据。", "現在のチェックではアプリの可用性を確認できません。経路と監視側の証拠を確認してください。"}, "Only cached evidence is available. Reconnect to obtain current samples.": {"只有缓存证据，请重新连接以获取当前样本。", "キャッシュされた証拠のみです。再接続して現在の測定値を取得してください。"}, "This topology has not supplied live evidence.": {"此拓扑尚未提供真实证据。", "このトポロジーには実際の証拠がありません。"},
}

func localizedVerdict(language string, v Verdict) Verdict {
	if language != "zh" && language != "ja" {
		return v
	}
	if strings.HasSuffix(v.Text, " INCIDENTS") || strings.HasSuffix(v.Text, " INCIDENT") {
		number, _, _ := strings.Cut(v.Text, " ")
		if language == "zh" {
			v.Text = number + " 个事件"
		} else {
			v.Text = number + " 件のインシデント"
		}
	} else if strings.HasSuffix(v.Text, " DOWN") {
		number, _, _ := strings.Cut(v.Text, " ")
		if language == "zh" {
			v.Text = number + " 项故障"
		} else {
			v.Text = number + " 件の障害"
		}
	} else {
		v.Text = Text(language, v.Text)
	}
	return v
}
func coverageLine(language string, c Coverage) string {
	observer := c.Observer
	if observer == "" {
		observer = map[string]string{"zh": "所有观测点", "ja": "すべての観測点"}[language]
	}
	switch language {
	case "zh":
		return fmt.Sprintf("服务可用性：%d/%d 已知 · 视角：%s", c.Known, c.Services, observer)
	case "ja":
		return fmt.Sprintf("サービス可用性：%d/%d 確認済み · 観測点：%s", c.Known, c.Services, observer)
	}
	return ""
}
