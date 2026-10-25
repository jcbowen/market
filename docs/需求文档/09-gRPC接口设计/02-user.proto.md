### user.proto

```protobuf
syntax = "proto3";

package user;

option go_package = "github.com/jcbowen/market/src/backend/common/proto/user";

import "common/common.proto";

service UserService {
  rpc GetUserById(GetUserByIdReq) returns (UserInfo);
  rpc GetUserByPhone(GetUserByPhoneReq) returns (UserInfo);
  rpc CreateOrUpdateUser(CreateUserReq) returns (UserInfo);
  rpc UpdateUserCommunity(UpdateCommunityReq) returns (common.CommonResponse);
  rpc ListUsers(ListUsersReq) returns (ListUsersResp);

  // 地址
  rpc ListAddresses(ListAddressesReq) returns (ListAddressesResp);
  rpc GetAddress(GetAddressReq) returns (AddressInfo);
  rpc CreateAddress(CreateAddressReq) returns (AddressInfo);
  rpc UpdateAddress(UpdateAddressReq) returns (AddressInfo);
  rpc DeleteAddress(DeleteAddressReq) returns (common.CommonResponse);
  rpc SetDefaultAddress(SetDefaultAddressReq) returns (common.CommonResponse);

  // 社区
  rpc ListCommunities(ListCommunitiesReq) returns (ListCommunitiesResp);
  rpc GetCommunity(GetCommunityReq) returns (CommunityInfo);
  rpc ListNearbyCommunities(ListNearbyCommunitiesReq) returns (ListCommunitiesResp);
  rpc CreateCommunity(CreateCommunityReq) returns (CommunityInfo);
  rpc UpdateCommunity(UpdateCommunityInfoReq) returns (CommunityInfo);
  rpc DeleteCommunity(DeleteCommunityReq) returns (common.CommonResponse);

  // 用户资料
  rpc UpdateUserProfile(UpdateUserProfileReq) returns (UserInfo);
  // UpdateMember 后台编辑会员基础资料（昵称/性别/生日，社区语境校验）
  rpc UpdateMember(UpdateMemberReq) returns (UserInfo);

  // 行政区划
  rpc ListRegionChildren(ListRegionChildrenReq) returns (ListRegionChildrenResp);
  rpc ListRegions(ListRegionsReq) returns (ListRegionsResp);
  rpc MatchRegionByName(MatchRegionByNameReq) returns (RegionMatchInfo);
  rpc CreateRegionUpdateTask(CreateRegionUpdateTaskReq) returns (RegionUpdateTaskInfo);
  rpc GetRegionUpdateTask(GetRegionUpdateTaskReq) returns (RegionUpdateTaskInfo);
  rpc ApplyRegionUpdateTask(ApplyRegionUpdateTaskReq) returns (common.CommonResponse);

  // 管理员
  rpc GetAdminByUsername(GetAdminByUsernameReq) returns (AdminInfo);
  rpc ListAdmins(ListAdminsReq) returns (ListAdminsResp);
  rpc CreateAdmin(CreateAdminReq) returns (AdminInfo);
  rpc UpdateAdmin(UpdateAdminReq) returns (AdminInfo);
  rpc DeleteAdmin(DeleteAdminReq) returns (common.CommonResponse);
  // GetAdminAssignments 查询账号任职的全部社区与角色（账号管理页任职详情弹窗，超管专用）
  rpc GetAdminAssignments(GetAdminAssignmentsReq) returns (GetAdminAssignmentsResp);
  rpc ChangeAdminPassword(ChangeAdminPasswordReq) returns (common.CommonResponse);
  // GetAdminContext 实时解析管理员在指定社区的权限上下文（任职角色权限并集）
  rpc GetAdminContext(GetAdminContextReq) returns (AdminContextInfo);

  // 角色
  rpc ListRoles(ListRolesReq) returns (ListRolesResp);
  rpc GetRole(GetRoleReq) returns (RoleInfo);
  rpc CreateRole(CreateRoleReq) returns (RoleInfo);
  rpc UpdateRole(UpdateRoleReq) returns (RoleInfo);
  rpc DeleteRole(DeleteRoleReq) returns (common.CommonResponse);

  // 会员-社区关联与状态
  // MarkUserCommunity 幂等记录会员与社区的关联（收货地址/下单/后台添加会员时调用）
  rpc MarkUserCommunity(MarkUserCommunityReq) returns (common.CommonResponse);
  // SetUserStatus 设置会员状态（拉黑/恢复）
  rpc SetUserStatus(SetUserStatusReq) returns (common.CommonResponse);
  // CreateMember 注册会员（手机号幂等注册到指定社区，已注册时复用并关联）
  rpc CreateMember(CreateMemberReq) returns (CreateMemberResp);

  // --- 履约人员（B 社区履约工作台） ---
  // 履约人员 = 本社区任职且绑定了会员的员工（admin_users.user_id），配置入口在员工编辑页
  // 列出某社区全部履约人员（后台订单指派弹窗数据源）
  rpc ListFulfillers(ListFulfillersReq) returns (ListFulfillersResp);
  // 校验用户在某社区是否履约人员（C 端工作台入口判断 + gateway 鉴权）
  rpc CheckFulfiller(CheckFulfillerReq) returns (CheckFulfillerResp);
}

message GetUserByIdReq {
  int64 id = 1;
  // 操作者社区语境：>0 时校验会员与本社区关联，无关联按不存在处理（防跨社区越权读取；超管传 0 放行）
  int64 community_id = 2;
}

message GetUserByPhoneReq {
  string phone = 1;
}

message CreateUserReq {
  string openid = 1;
  string alipay_user_id = 2;
  string union_id = 3;
  string phone = 4;
  string nickname = 5;
  string avatar = 6;
  int32 gender = 7;
  int64 community_id = 8;
}

message UpdateCommunityReq {
  int64 user_id = 1;
  int64 community_id = 2;
}

message UserInfo {
  int64 id = 1;
  string openid = 2;
  string alipay_user_id = 3;
  string union_id = 4;
  string phone = 5;
  string nickname = 6;
  string avatar = 7;
  int32 gender = 8;
  int64 community_id = 9;
  int32 status = 10;
  string last_login_at = 11;
  string created_at = 12;
  // 当前过滤语境下的社区名称（ListUsers 按社区过滤时由服务端填充）
  string community_name = 13;
  // 生日（YYYY-MM-DD，未设置时为空串）
  string birthday = 14;
}

message UpdateUserProfileReq {
  int64 id = 1;
  string nickname = 2;
  string avatar = 3;
  int32 gender = 4;
  // 生日（YYYY-MM-DD，非空才覆盖）
  string birthday = 5;
}

// UpdateMemberReq 后台编辑会员基础资料请求
message UpdateMemberReq {
  // 会员 ID
  int64 id = 1;
  // 昵称（非空才覆盖）
  string nickname = 2;
  // 性别：全量覆盖（0未知 1男 2女）
  int32 gender = 3;
  // 生日：全量覆盖，空串表示清空
  string birthday = 4;
  // 操作者社区语境：>0 时校验会员与本社区关联，防止跨社区修改他社区会员（超管传 0 放行）
  int64 community_id = 5;
}

message ListUsersReq {
  int64 community_id = 1;
  string keyword = 2;
  int32 status = 3;
  // 状态筛选：-1 全部，0 拉黑，1 正常（提供时优先于 status 字段）
  int32 status_filter = 5;
  common.Pagination pagination = 4;
}

message ListUsersResp {
  repeated UserInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message ListAddressesReq {
  int64 user_id = 1;
}

message GetAddressReq {
  int64 id = 1;
  int64 user_id = 2;
}

message CreateAddressReq {
  int64 user_id = 1;
  string contact_name = 2;
  string contact_phone = 3;
  string province = 4;
  string city = 5;
  string district = 6;
  string detail = 7;
  string tag = 8;
  bool is_default = 9;
  // 地址所属社区（服务端按用户当前社区回填，前端传值会被服务端覆盖）
  int64 community_id = 10;
  // 省/市/区行政区划代码（来自区划选择器，可选；历史数据允许为空）
  string province_code = 11;
  string city_code = 12;
  string district_code = 13;
}

message UpdateAddressReq {
  int64 id = 1;
  int64 user_id = 2;
  string contact_name = 3;
  string contact_phone = 4;
  string province = 5;
  string city = 6;
  string district = 7;
  string detail = 8;
  string tag = 9;
  bool is_default = 10;
  // 地址所属社区（服务端按用户当前社区回填，前端传值会被服务端覆盖）
  int64 community_id = 11;
  // 省/市/区行政区划代码（来自区划选择器，可选；历史数据允许为空）
  string province_code = 12;
  string city_code = 13;
  string district_code = 14;
}

message DeleteAddressReq {
  int64 id = 1;
  int64 user_id = 2;
}

message SetDefaultAddressReq {
  int64 id = 1;
  int64 user_id = 2;
}

message AddressInfo {
  int64 id = 1;
  int64 user_id = 2;
  string contact_name = 3;
  string contact_phone = 4;
  string province = 5;
  string city = 6;
  string district = 7;
  string detail = 8;
  string tag = 9;
  bool is_default = 10;
  string created_at = 11;
  // 地址所属社区 ID 与名称（按保存时用户当前社区回填，供 C 端按社区分组展示）
  int64 community_id = 12;
  string community_name = 13;
  // 省/市/区行政区划代码（历史数据允许为空）
  string province_code = 14;
  string city_code = 15;
  string district_code = 16;
}

message ListAddressesResp {
  repeated AddressInfo list = 1;
}

message GetCommunityReq {
  int64 id = 1;
}

message CommunityInfo {
  int64 id = 1;
  string name = 2;
  string address = 3;
  double longitude = 4;
  double latitude = 5;
  string contact_phone = 6;
  string pickup_address = 7;
  double delivery_fee = 8;
  double min_order_amount = 9;
  double delivery_range_km = 10;
  int32 status = 11;
  string business_hours = 12;
  int32 sort = 13;
  string manager_name = 14;
  string manager_phone = 15;
  // 社区所在省/市/区（C 端锁定地址前缀用）
  string province = 16;
  string city = 17;
  string district = 18;
  // 是否允许社区外配送（false 时 C 端地址前缀锁定为本社区）
  bool allow_outside_delivery = 19;
  // 省/市/区行政区划代码（历史数据允许为空）
  string province_code = 20;
  string city_code = 21;
  string district_code = 22;
  // 取货渠道开关（社区级配置，C 端与收银台共用；到店不在此列，收银台恒可用）
  bool enable_self_pickup = 23;
  bool enable_delivery = 24;
}

// ListCommunitiesReq 社区列表查询请求
message ListCommunitiesReq {
  common.Pagination pagination = 1;
  // 管理端置 true 时查询包含停业社区；默认 false 仅返回营业中（客户端语义）
  bool include_disabled = 2;
  // 状态筛选（仅 include_disabled=true 时生效）：-1 全部，0 停业，1 营业
  int32 status = 3;
}

message CreateCommunityReq {
  string name = 1;
  string address = 2;
  double longitude = 3;
  double latitude = 4;
  string contact_phone = 5;
  string pickup_address = 6;
  double delivery_fee = 7;
  double min_order_amount = 8;
  double delivery_range_km = 9;
  int32 status = 10;
  string business_hours = 11;
  int32 sort = 12;
  string manager_name = 13;
  string manager_phone = 14;
  string province = 15;
  string city = 16;
  string district = 17;
  // 是否允许社区外配送，默认 false
  bool allow_outside_delivery = 18;
  // 省/市/区行政区划代码（来自区划选择器，可选）
  string province_code = 19;
  string city_code = 20;
  string district_code = 21;
}

message UpdateCommunityInfoReq {
  int64 id = 1;
  string name = 2;
  string address = 3;
  double longitude = 4;
  double latitude = 5;
  string contact_phone = 6;
  string pickup_address = 7;
  double delivery_fee = 8;
  double min_order_amount = 9;
  double delivery_range_km = 10;
  int32 status = 11;
  string business_hours = 12;
  int32 sort = 13;
  string manager_name = 14;
  string manager_phone = 15;
  string province = 16;
  string city = 17;
  string district = 18;
  // 是否允许社区外配送（整表提交全量覆盖）
  bool allow_outside_delivery = 19;
  // 省/市/区行政区划代码（来自区划选择器，可选）
  string province_code = 20;
  string city_code = 21;
  string district_code = 22;
  // 取货渠道开关（整表提交全量覆盖；超管社区编辑接口需回填当前值防误关）
  bool enable_self_pickup = 23;
  bool enable_delivery = 24;
}

message DeleteCommunityReq {
  int64 id = 1;
}

message ListNearbyCommunitiesReq {
  double longitude = 1;
  double latitude = 2;
  common.Pagination pagination = 3;
}

message ListCommunitiesResp {
  repeated CommunityInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message GetAdminByUsernameReq {
  string username = 1;
}

message AdminInfo {
  int64 id = 1;
  string username = 2;
  string password = 3;
  string real_name = 4;
  string phone = 5;
  // 以下 role_id / role_code / role_name / community_id 为旧单角色模型遗留字段，仅做兼容填充
  int64 role_id = 6;
  string role_code = 7;
  string role_name = 8;
  int64 community_id = 9;
  // 任职的全部社区 ID 列表（超管为空，表示不限制社区）
  repeated int64 community_ids = 10;
  int32 status = 11;
  string last_login_at = 12;
  string last_login_ip = 13;
  // 是否超级管理员（新模型：超管标记在账号上，不再依赖全局角色）
  bool is_super = 14;
  // 在查询语境社区内担任的角色列表（ListAdmins 按社区语境填充）
  repeated RoleInfo roles = 15;
  // 绑定的会员 ID（0=未绑定；绑定后该会员在任职社区即履约人员）
  int64 user_id = 16;
  // 绑定会员的昵称/手机号（列表展示与编辑回显用，ListAdmins 批量填充）
  string user_nickname = 17;
  string user_phone = 18;
}

message ListAdminsReq {
  // 任职社区过滤：>0 只列出在该社区任职的员工；0 列出全部账号
  int64 community_id = 1;
  common.Pagination pagination = 2;
  // 关键词：用户名/姓名/手机号模糊匹配
  string keyword = 3;
}

message ListAdminsResp {
  repeated AdminInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message CreateAdminReq {
  string username = 1;
  // 新建账号时的初始密码；username 已存在（复用账号）时忽略
  string password = 2;
  string real_name = 3;
  string phone = 4;
  // 旧单角色字段，已废弃不用
  int64 role_id = 5;
  // 任职社区（员工管理语境下由网关注入当前社区）
  int64 community_id = 6;
  // 旧多社区全量绑定字段，已废弃（任职关系改为按社区 + 角色列表）
  repeated int64 community_ids = 7;
  // 在 community_id 社区内担任的角色 ID 列表（须为该社区的角色）
  repeated int64 role_ids = 8;
  // 仅创建账号不在任何社区任职（超管全局账号管理用，community_id 传 0）
  bool account_only = 10;
  // 指定为目标社区的内置社区管理员（服务端自动解析/创建内置角色，忽略 role_ids）
  bool assign_community_admin = 11;
  // 创建时绑定的会员 ID（0=不绑定；绑定后该会员在任职社区即履约人员）
  int64 user_id = 12;
  // 操作者是否超管（网关注入；决定能否分配内置社区管理员角色）
  bool operator_is_super = 9;
}

message UpdateAdminReq {
  int64 id = 1;
  string real_name = 2;
  string phone = 3;
  // 旧单角色字段，已废弃不用
  int64 role_id = 4;
  // 任职社区语境（>0 时按该社区全量覆盖角色任职）
  int64 community_id = 5;
  int32 status = 6;
  // 旧多社区全量绑定字段，已废弃
  repeated int64 community_ids = 7;
  // 在 community_id 社区内担任的角色 ID 列表（全量覆盖该社区任职）
  repeated int64 role_ids = 8;
  // 操作者是否超管（网关注入）
  bool operator_is_super = 9;
  // 仅更新状态（超管账号管理：停用/启用，不触碰姓名/手机号/角色任职）
  bool update_status_only = 10;
  // 绑定的会员 ID（三态：未传=不改动；0=解绑；>0=绑定/换绑该会员）
  optional int64 user_id = 11;
}

message DeleteAdminReq {
  int64 id = 1;
  // 任职社区语境：>0 仅移除该社区任职（账号保留）；0 移除全部任职
  int64 community_id = 2;
}

// GetAdminAssignmentsReq 账号任职详情查询
message GetAdminAssignmentsReq {
  // 管理员（账号）ID
  int64 admin_id = 1;
}

// AdminAssignment 单个任职社区及其角色列表
message AdminAssignment {
  int64 community_id = 1;
  string community_name = 2;
  // 在该社区担任的角色列表
  repeated RoleInfo roles = 3;
}

message GetAdminAssignmentsResp {
  // 按社区 ID 升序
  repeated AdminAssignment list = 1;
}

message ChangeAdminPasswordReq {
  int64 id = 1;
  string old_password = 2;
  string new_password = 3;
  // 超管重置密码：跳过旧密码校验（由网关校验操作者身份后注入）
  bool force = 4;
}

// GetAdminContextReq 实时权限上下文查询
message GetAdminContextReq {
  int64 admin_id = 1;
  // 当前社区语境（X-Community-Id）
  int64 community_id = 2;
}

// AdminContextInfo 管理员在指定社区的权限上下文
message AdminContextInfo {
  bool is_super = 1;
  // 在该社区担任的角色 ID 列表
  repeated int64 role_ids = 2;
  // 任职角色权限并集；含 "*" 通配
  repeated string permissions = 3;
  // 是否在该社区有任职（用于越权社区切换判定）
  bool bound = 4;
}

message RoleInfo {
  int64 id = 1;
  string name = 2;
  string code = 3;
  repeated string permissions = 4;
  string description = 5;
  // 所属社区：>0=社区私有角色（业务唯一口径）；0=历史保留值，仅存量兼容数据
  int64 community_id = 6;
  // 所属社区名称：仅 all_communities 跨社区查询时回填
  string community_name = 7;
  // 该角色当前任职人数：仅 all_communities 跨社区查询时回填
  int64 assignment_count = 8;
}

message ListRolesReq {
  // 社区语境：>0 返回该社区角色；0 返回历史保留的全局行
  int64 community_id = 1;
  // 是否附带全局角色（超管视角）
  bool include_global = 2;
  // 跨社区总览：忽略 community_id，返回全部社区的角色并回填社区名与任职人数（仅超管可用）
  bool all_communities = 3;
}

message ListRolesResp {
  repeated RoleInfo list = 1;
}

message GetRoleReq {
  int64 id = 1;
  // 操作者社区语境：>0 时服务端强校验角色归属社区（超管传 0 放行）
  int64 community_id = 2;
}

message CreateRoleReq {
  string name = 1;
  string code = 2;
  repeated string permissions = 3;
  string description = 4;
  // 所属社区：必填且须 >0，角色一律社区私有（0 会被服务端拒绝）
  int64 community_id = 5;
}

message UpdateRoleReq {
  int64 id = 1;
  string name = 2;
  string code = 3;
  repeated string permissions = 4;
  string description = 5;
}

message DeleteRoleReq {
  int64 id = 1;
}

// --- 行政区划 ---

// RegionInfo 行政区划条目
message RegionInfo {
  // 行政区划代码（GB/T 2260），省级 2 位、市级 4 位、区县级 6 位
  string code = 1;
  string name = 2;
  // 父级区划代码，省级为 "0"
  string parent_code = 3;
  // 层级：1 省 2 市 3 区县
  int32 level = 4;
  // 状态：1 正常 2 停用（上游撤销）
  int32 status = 5;
}

message ListRegionChildrenReq {
  // 父级区划代码，"0" 表示查询省级列表；仅返回正常状态
  string parent_code = 1;
}

message ListRegionChildrenResp {
  repeated RegionInfo list = 1;
}

message ListRegionsReq {
  // 关键词：区划代码前缀或名称模糊匹配
  string keyword = 1;
  // 父级区划代码筛选，空为全部
  string parent_code = 2;
  // 层级筛选：0 全部，1 省，2 市，3 区县
  int32 level = 3;
  // 状态筛选：0 全部，1 正常，2 停用
  int32 status = 4;
  common.Pagination pagination = 5;
}

message ListRegionsResp {
  repeated RegionInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message MatchRegionByNameReq {
  // 地理库返回的名称（城市或省份，如 "广州"、"广东"），不带行政区划后缀亦可
  string name = 1;
}

// RegionMatchInfo 名称匹配结果：city 命中时同时返回其上级 province；
// 仅 province 命中时 city 为空
message RegionMatchInfo {
  RegionInfo province = 1;
  RegionInfo city = 2;
}

// RegionDiffItem 更新任务差异条目
message RegionDiffItem {
  string code = 1;
  // 库内当前名称（新增场景为上游名称）
  string name = 2;
  // 更名后的新名称（仅 updated 场景有值）
  string new_name = 3;
  string parent_code = 4;
  int32 level = 5;
}

// RegionUpdateTaskInfo 更新任务信息
message RegionUpdateTaskInfo {
  int64 id = 1;
  // 状态：1 待执行 2 拉取中 3 比对中 4 完成 5 失败
  int32 status = 2;
  // 数据源 URL
  string source = 3;
  int32 total_remote = 4;
  int32 total_local = 5;
  int32 added_count = 6;
  int32 updated_count = 7;
  int32 removed_count = 8;
  repeated RegionDiffItem added = 9;
  repeated RegionDiffItem updated = 10;
  repeated RegionDiffItem removed = 11;
  // 失败原因（status=5 时有值）
  string error = 12;
  // 差异是否已覆盖入库
  bool applied = 13;
  string created_at = 14;
  string finished_at = 15;
}

message GetRegionUpdateTaskReq {
  int64 id = 1;
}

// CreateRegionUpdateTaskReq 创建区划更新任务请求
message CreateRegionUpdateTaskReq {
  // 数据源类型：default 常规多源镜像（默认）/ official 民政部官方源（兜底修正）
  string source_type = 1;
}

message ApplyRegionUpdateTaskReq {
  int64 id = 1;
}

// --- 会员-社区关联与会员管理 ---

// MarkUserCommunityReq 会员-社区关联记录请求（幂等，重复记录不报错）
message MarkUserCommunityReq {
  int64 user_id = 1;
  int64 community_id = 2;
  // 记录来源：address 收货地址 / order 下单 / admin 后台添加 / backfill 存量回填
  string source = 3;
}

// SetUserStatusReq 设置会员状态请求
message SetUserStatusReq {
  int64 id = 1;
  // 0 拉黑 1 正常
  int32 status = 2;
}

// CreateMemberReq 注册会员请求
message CreateMemberReq {
  // 手机号（唯一；已注册时不报错，复用既有会员并关联本社区）
  string phone = 1;
  string nickname = 2;
  // 0未知 1男 2女
  int32 gender = 3;
  // 注册归属社区（网关当前社区语境）
  int64 community_id = 4;
  // 会员-社区关联来源：admin 后台添加 / pos 收银台（缺省 admin）
  string source = 5;
  // 生日（YYYY-MM-DD，选填）
  string birthday = 6;
}

// CreateMemberResp 注册会员响应
message CreateMemberResp {
  // 注册（或复用）的会员信息
  UserInfo user = 1;
  // 是否本次新注册（false=手机号已存在，复用既有会员并关联本社区）
  bool is_new = 2;
}

// --- 履约人员（B 社区履约工作台） ---

// FulfillerInfo 履约人员信息（指派弹窗展示）
message FulfillerInfo {
  int64 user_id = 1;
  string nickname = 2;
  string phone = 3;
}

message ListFulfillersReq {
  int64 community_id = 1;
}

message ListFulfillersResp {
  repeated FulfillerInfo list = 1;
}

// CheckFulfillerReq 校验履约人员请求
message CheckFulfillerReq {
  int64 user_id = 1;
  int64 community_id = 2;
}

message CheckFulfillerResp {
  bool is_fulfiller = 1;
}
```

接口语义说明（补充相对旧版文档的新增 RPC）：

- `GetAdminContext`：网关在每次管理端请求时实时调用，解析管理员在 `X-Community-Id` 社区的任职与权限并集（含 `*` 通配），供 `RequireCommunityAccess`（越权社区切换拦截）与 `RequirePermission`（权限点校验）中间件使用；`bound=false` 表示在该社区无任职，直接 403。
- `GetRole` / `ListRoles(ListRolesReq)`：角色详情与分语境列表；`community_id>0` 返回该社区私有角色，`0` 返回历史保留的全局行（`community_id=0` 的存量数据，已无业务消费方），`include_global=true`（超管视角）附带全局行；`all_communities=true`（仅超管，网关 `?scope=all` 映射）忽略社区语境返回全部社区角色，并批量回填 `community_name`、`assignment_count`（各一条聚合查询，无 N+1），用于超管「社区角色」跨社区只读总览。
- `CreateRole`：`community_id` 必填且须 `>0`，角色一律社区私有；传 0 由服务端拒绝（历史「全局角色」不再可新建）。
- `MarkUserCommunity`：会员-社区关联幂等记录（user_communities 表），来源含 address/order/admin/backfill，重复记录不报错。
- `SetUserStatus` / `CreateMember`：会员拉黑/恢复与按手机号幂等注册（收银台与后台均可用）。`CreateMember` 以手机号为唯一键：未注册时新建会员并归属当前社区语境；已注册时不报错，复用既有会员并把 `user_communities` 关联到当前社区，`source` 标记关联来源（`admin` 后台添加 / `pos` 收银台，缺省 `admin`）。响应 `CreateMemberResp` 返回 `user`（注册或复用的会员信息）与 `is_new`（是否本次新注册）。
- `CreateOrUpdateUser`（C 端登录链路共用：手机号登录 / 微信登录）：昵称只在为空时写入——新建会员按统一口径补默认值（有手机号为「会员+尾号 4 位」，无手机号为「微信用户」，与 `CreateMember` 一致），已有昵称不会被重复登录覆盖，用户改名走 `UpdateUserProfile`。
- `ListFulfillers` / `CheckFulfiller`：履约人员（B 履约工作台）列表与实时校验；`CheckFulfiller` 同时用于 C 端工作台入口判断与网关履约接口鉴权。履约人员 = 本社区任职中且绑定会员的员工（`CreateAdminReq.user_id` / `UpdateAdminReq.user_id` 配置，随任职增减自动生效），不再有独立的会员侧标记。

### 客户端渠道管理（2026-10-18 新增 RPC）

- `ListClientChannels(only_enabled)` / `GetClientChannel(code)` / `SaveClientChannel`：客户端渠道主数据（client_channels 表：mp 微信小程序 / gzh 微信公众号 / app 开放平台App / h5 浏览器H5）的查询与维护；`SaveClientChannel` 的 AppSecret 留空保留原值，code 不可变更，返回 `ClientChannelInfo`（含 `configured` 凭证完整性，h5 恒 true）。
- `GetUserChannelOpenid(user_id, channel_code)`：查询用户在指定渠道的微信 openid/unionid；渠道身份表（user_channel_identities）优先，mp 渠道回落存量 `users.openid`（排除 phone_ 占位），零迁移兼容。
- `UpsertUserChannelIdentity(user_id, channel_code, openid, unionid)`：登录成功后幂等写入/更新用户渠道身份；内容一致时跳过写入，并发唯一索引冲突视为已存在。
- 服务归属说明：渠道本质是「客户端身份与准入」（登录换 openid、按端授权、未来的渠道访问开关），与 identities 同域闭环在 user 服务；支付仅是消费场景，payment 侧不感知渠道表，按网关传入的 app_id+openid 下单。

### 站点设置（2026-10-20 新增 RPC）

```protobuf
  // 站点设置：平台级品牌与部署域名（单行配置，仅超管可写）
  // base_url 是部署域名的唯一来源，网关据此派生支付回调地址、附件外链与 HTML 品牌注入
  rpc GetSiteConfig(GetSiteConfigReq) returns (SiteConfigInfo);
  rpc SaveSiteConfig(SaveSiteConfigReq) returns (SiteConfigInfo);
```

- `GetSiteConfig`：读取 `site_configs` 单行配置（固定主键 id=1），行不存在时惰性创建后返回，调用方恒能拿到非 nil 结果；并发创建靠主键冲突后重读兜底，不会产生多行。
- `SaveSiteConfig`：覆盖式写入，**传空串即清空该项**（与 `SaveClientChannel` 的「留空保留原值」口径不同，品牌字段需要能主动清空）。服务端校验：`base_url` 归一为 `scheme://host[:port]`（小写协议、去尾斜杠，缺协议/带路径参数/非 http(s) 返回 `InvalidArgument`）、`site_name` ≤32 字、`seo_description`/`seo_keywords` ≤200 字、`footer_html` 白名单消毒后 ≤2000 字符（仅放行 a/b/strong/i/em/u/p/br/span 与合规 href，脚本/事件属性/危险协议一律剥除）、`logo_url`/`favicon_url` 须为站内绝对路径或 http(s) 外链。
- `SiteConfigInfo`：七个配置项（含 `footer_html`，原 `copyright_text`/`icp_number` 两字段已合并，proto 字段号不复用）+ `version`（每次保存 +1）+ `updated_at`。`version` 是网关内存快照与 favicon 长缓存的失效依据，故必须每次保存递增，不随内容是否变化而定。
- 服务归属说明：与 `client_channels` 同理——表放在 user 服务（平台级单行配置、与社区无隔离关系），消费方几乎全在网关（品牌快照、回调地址派生、附件外链基址、HTML 注入），payment 侧只被动接收派生结果，不新增 payment 侧配置入口。

