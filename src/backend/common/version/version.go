package version

// AppVersion 项目版本号
// 构建时经 -ldflags 注入，注入路径为完整变量名：
//
//	go build -ldflags "-X github.com/jcbowen/market/src/backend/common/version.AppVersion=v0.1.3" ./gateway/
//
// 默认值 dev：本地 go run / IDE 直接运行未注入时展示 dev，
// 正式产物由 build.mjs（本地取 git tag）与 CI release job（取 GITHUB_REF_NAME）注入
var AppVersion = "dev"
