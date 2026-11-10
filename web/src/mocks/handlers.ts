import type { RequestHandler } from 'msw'

/**
 * MSW 请求处理器。
 *
 * W0 阶段刻意留空：仓库尚无 contracts/openapi.yaml，前端不自造业务接口。
 * 后端 B0 提供正式契约后，再按契约补充商品、购物车、订单等 handler。
 * 单元测试可在用例内用 server.use() 临时注册 handler。
 */
export const handlers: RequestHandler[] = []
