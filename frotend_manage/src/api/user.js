import request from '@/utils/request'

export function login(data) {
  return request({
    // url: '/backend/admin/login',
    url: '/backend/login',
    method: 'post',
    data
  })
}

export function getInfo(token) {
  return request({
    url: '/backend/admin/info',
    method: 'get',
    params: { token }
  })
}

export function logout(data) {
  return request({
    url: '/backend/user/logout',
    method: 'post',
    data
  })
}
