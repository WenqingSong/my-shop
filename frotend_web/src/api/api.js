import request from "@/utils/request";
import { query } from "./query";

//头部卡片
export function cardList(data) {
  return request({
    url: "/backend/dashboard/head".concat('?', query(data)),
    method: "get",
  });
}

//echarts
export function chartsList(data) {
  return request({
    url: "/backend/dashboard/echarts",
    method: "post",
    data,
  });
}

//轮播图
export function loopList(data) {
  return request({
    url: "/backend/rotation/list".concat('?', query(data)),
    method: "get",
  });
}
//轮播图删除
export function loopDelete(data) {
  return request({
    url: "/backend/rotation/delete",
    method: "delete",
    data,
  });
}
//轮播图新增
export function loopAdd(data) {
  return request({
    url: "/backend/rotation/add",
    method: "post",
    data,
  });
}
//轮播图编辑
export function loopEdit(data) {
  return request({
    url: "/backend/rotation/update",
    method: "post",
    data,
  });
}

//商品列表
export function comList(data) {
  return request({
    url: "/backend/goods/list".concat('?', query(data)),
    method: "get",
  });
}
//商品添加
export function comAdd(data) {
  return request({
    url: "/backend/goods/add",
    method: "post",
    data,
  });
}
//商品编辑
export function comUpdate(data) {
  return request({
    url: "/backend/goods/update",
    method: "post",
    data,
  });
}
//商品详情
export function comDetail(data) {
  return request({
    url: "/backend/goods/detail".concat('?', query(data)),
    method: "get",
  });
}
//商品删除
export function comDelete(data) {
  return request({
    url: "/backend/goods/delete",
    method: "delete",
    data,
  });
}

//商品分类
export function classList(data) {
  return request({
    url: "/backend/category/list".concat('?', query(data)),
    method: "get",
  });
}
//新增分类
export function classAdd(data) {
  return request({
    url: "/backend/category/add",
    method: "post",
    data,
  });
}
//删除分类
export function classDelete(data) {
  return request({
    url: "/backend/category/delete",
    method: "delete",
    data,
  });
}

//优惠卷
export function couponList(data) {
  return request({
    url: "/backend/coupon/list",
    method: "post",
    data,
  });
}
//优惠卷删除
export function couponDelete(data) {
  return request({
    url: "/backend/coupon/delete",
    method: "post",
    data,
  });
}
//优惠卷添加
export function couponAdd(data) {
  return request({
    url: "/backend/coupon/add",
    method: "post",
    data,
  });
}
//优惠卷添加
export function couponUpdate(data) {
  return request({
    url: "/backend/coupon/update",
    method: "post",
    data,
  });
}

//评价列表
export function evaluateList(data) {
  return request({
    url: "/backend/comment/list".concat('?', query(data)),
    method: "get",
  });
}
//删除评价
export function evaluateDelete(data) {
  return request({
    url: "/backend/comment/delete",
    method: "delete",
    data,
  });
}

//订单列表
export function orderList(data) {
  return request({
    url: "/backend/order/list".concat('?', query(data)),
    method: "get",
  });
}

//用户列表（user_info表）
export function memberList(data) {
  return request({
    url: "/backend/user/list".concat('?', query(data)),
    method: "get",
  });
}
//冻结/解冻用户
export function memberUpdate(data) {
  return request({
    url: "/backend/user/status",
    method: "post",
    data,
  });
}
//删除用户
export function memberDelete(data) {
  return request({
    url: "/backend/user/delete",
    method: "delete",
    data,
  });
}
//提升用户为管理员
export function promoteToAdmin(data) {
  return request({
    url: "/backend/user/promote",
    method: "post",
    data,
  });
}



//种草管理
export function recommendList(data) {
  return request({
    url: "/backend/article/list".concat('?', query(data)),
    method: "get",
  });
}
//种草管理
export function recommendDelete(data) {
  return request({
    url: "/backend/article/delete",
    method: "delete",
    data,
  });
}
//种草编辑
export function recommendUpdata(data) {
  return request({
    url: "/backend/article/update",
    method: "post",
    data,
  });
}
//新增种草
export function recommendAdd(data) {
  return request({
    url: "/backend/article/add",
    method: "post",
    data,
  });
}

//新增种草
export function levelList(data) {
  return request({
    url: "/backend/category/list".concat('?', query(data)),
    method: "get",
  });
}
//地址管理
export function addressList(data) {
  return request({
    url: "/backend/consignee/list".concat('?', query(data)),
    method: "get",
  });
}
//删除地址
export function addressDelete(data) {
  return request({
    url: "/backend/consignee/delete",
    method: "delete",
    data,
  });
}
//修改密码
export function updataPassword(data) {
  return request({
    url: "/backend/admin/update/my/password",
    method: "post",
    data,
  });
}

/**
 * 获取角色列表
 * @param {{limit:number,page:number,keyword?:string}} data
 */
export function getRoleList(data) {
  return request({ url: "/backend/role/list".concat('?', query(data)), method: "get" });
}

/**
 * 新增角色
 * @param {{name:string,desc:string}} data
 */
export function addRole(data) {
  return request({ url: "/backend/role/add", method: "post", data });
}

/**
 * 删除角色
 * @param {number} id
 */
export function deleteRole(id) {
  return request({ url: "/backend/role/delete", method: "delete", data: { id } });
}

/**
 * 编辑角色
 * @param {{name:string,desc:string,id:number}} data
 */
export function editRole(data) {
  return request({ url: "/backend/role/update", method: "post", data });
}

/**
 * 批量添加角色权限
 * @param {{role_id:number,permission_ids:number[]}} data
 */
export function editRolePermission(data) {
  return request({ url: "/backend/role/add/permissions", method: "post", data });
}

/**
 * 批量删除角色权限
 * @param {{role_id:number,permission_ids:number[]}} data
 */
export function deleteRolePermission(data) {
  return request({ url: "/backend/role/delete/permissions", method: "delete", data });
}

/**
 * 获取权限列表
 * @param {{limit:number,page:number,keyword:string}} data
 */
export function getPermissionList(data) {
  return request({ url: "/backend/permission/list".concat('?', query(data)), method: "get" });
}

/**
 * 添加权限
 * @param {{name:string,path:string}} data
 */
export function addPermission(data) {
  return request({ url: "/backend/permission/add", method: "post", data });
}

/**
 * 删除权限
 * @param {number} id
 */
export function deletePermission(id) {
  return request({
    url: "/backend/permission/delete",
    method: "delete",
    data: { id },
  });
}

/**
 * 编辑权限
 * @param {{name:string,path:string,id:number}} data
 */
export function editPermission(data) {
  return request({ url: "/backend/permission/update", method: "post", data });
}

/**
 * 获取管理员列表
 * @param {{limit:number,page:number,keyword?:string}} data
 */
export function getAdminList(data) {
  return request({ url: "/backend/admin/list".concat('?', query(data)), method: "get" });
}

/**
 * 添加管理员
 * @param {{name:string,password:string,role_id:number,is_admin?:number}} data
 */
export function addAdmin(data) {
  return request({ url: "/backend/admin/add", method: "post", data });
}

/**
 * 编辑管理员
 * @param {{name?:string,password?:string,role_ids?:string,admin_id:number,is_admin?:number}} data
 */
export function editAdmin(data) {
  return request({ url: "/backend/admin/update", method: "post", data });
}

/**
 * 删除管理员
 * @param {number} adminId
 */
export function deleteAdmin(adminId) {
  return request({
    url: "/backend/admin/delete",
    method: "delete",
    data: { admin_id: adminId },
  });
}
