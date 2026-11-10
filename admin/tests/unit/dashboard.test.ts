import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import {
  getDashboardFlashSales,
  getDashboardOrderStatus,
  getDashboardOrderTrend,
  getDashboardOverview,
  getDashboardTopProducts,
  getDefaultDashboardStatusQuery
} from "@/api/modules/dashboard";
import { useAsyncSection } from "@/views/dashboard/composables/useAsyncSection";
import {
  buildOrderStatusOption,
  buildOrderTrendOption
} from "@/views/dashboard/utils/chart-options";
import { formatCentsAsCny, formatCentsAsYuan } from "@/utils/money";
import { formatThousands } from "@/utils/number";
import {
  formatOrderStatus,
  isKnownOrderStatus
} from "@/constants/order-status";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { getRecentDaysRFC3339, toRFC3339 } from "@/utils/datetime";

describe("运营工作台数据映射", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    clearAccessToken();
    setAccessToken(MOCK_ACCESS_TOKEN, 3600);
  });

  describe("五个 Dashboard API", () => {
    it("概览：解析六个指标字段", async () => {
      const data = await getDashboardOverview();

      expect(data).toEqual({
        user_total: 1280,
        today_new_users: 36,
        product_total: 240,
        order_total: 5821,
        today_new_orders: 128,
        today_order_amount: 2153650
      });
    });

    it("订单趋势：解析 days 数组", async () => {
      const data = await getDashboardOrderTrend();

      expect(data.days).toHaveLength(7);
      expect(data.days[0]).toEqual({ date: "2026-11-04", order_count: 42 });
      expect(data.days[6].order_count).toBe(128);
    });

    it("订单状态分布：解析 total 与 7 个状态", async () => {
      const data = await getDashboardOrderStatus(
        getDefaultDashboardStatusQuery()
      );

      expect(data.total).toBe(545);
      expect(data.status_distribution).toHaveLength(7);
      expect(data.status_distribution.map(item => item.status)).toEqual([
        10, 20, 30, 40, 50, 60, 70
      ]);
    });

    it("热销商品：解析 rank / product_id / product_name / sales", async () => {
      const data = await getDashboardTopProducts(10);

      expect(data.items).toHaveLength(3);
      expect(data.items[0]).toEqual({
        rank: 1,
        product_id: 1001,
        product_name: "演示商品 A",
        sales: 320
      });
    });

    it("秒杀统计：解析活动与请求状态分布", async () => {
      const data = await getDashboardFlashSales();

      expect(data.activity_total).toBe(12);
      expect(data.activity_status).toEqual({ enabled: 5, disabled: 7 });
      expect(data.request_status).toEqual({
        queued: 18,
        success: 940,
        failed: 32,
        dead: 3
      });
      expect(data.current_queued).toBe(18);
      expect(data.success_orders).toBe(902);
    });

    it("状态分布默认查询最近 7 天，参数为 RFC3339 且成对出现", () => {
      const query = getDefaultDashboardStatusQuery();

      expect(Object.keys(query).sort()).toEqual(["end_time", "start_time"]);
      const rfc3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2}$/;
      expect(query.start_time).toMatch(rfc3339);
      expect(query.end_time).toMatch(rfc3339);
      expect(new Date(query.start_time).getTime()).toBeLessThan(
        new Date(query.end_time).getTime()
      );
    });

    it("时间窗为左闭右开的 7 天（含今天）", () => {
      const now = new Date(2026, 10, 10, 15, 30, 0);
      const range = getRecentDaysRFC3339(7, now);

      expect(range.start_time).toBe(toRFC3339(new Date(2026, 10, 4, 0, 0, 0)));
      expect(range.end_time).toBe(toRFC3339(new Date(2026, 10, 11, 0, 0, 0)));
    });
  });

  describe("金额与数字格式化", () => {
    it("分转元保留两位小数", () => {
      expect(formatCentsAsYuan(2153650)).toBe("21,536.50");
      expect(formatCentsAsYuan(0)).toBe("0.00");
      expect(formatCentsAsYuan(5)).toBe("0.05");
      expect(formatCentsAsYuan(100)).toBe("1.00");
    });

    it("带货币符号展示", () => {
      expect(formatCentsAsCny(2153650)).toBe("¥21,536.50");
    });

    it("整数千分位", () => {
      expect(formatThousands(5821)).toBe("5,821");
      expect(formatThousands(0)).toBe("0");
      expect(formatThousands(1234567)).toBe("1,234,567");
    });
  });

  describe("订单状态映射", () => {
    it("使用后端真实状态机枚举名称", () => {
      expect(formatOrderStatus(10)).toBe("待支付");
      expect(formatOrderStatus(20)).toBe("已支付");
      expect(formatOrderStatus(30)).toBe("已发货");
      expect(formatOrderStatus(40)).toBe("已收货");
      expect(formatOrderStatus(50)).toBe("已完成");
      expect(formatOrderStatus(60)).toBe("已取消");
      expect(formatOrderStatus(70)).toBe("已退款");
    });

    it("未知状态不猜测语义，回退为原始状态码", () => {
      expect(formatOrderStatus(90)).toBe("状态 90");
      expect(isKnownOrderStatus(90)).toBe(false);
      expect(isKnownOrderStatus(20)).toBe(true);
    });
  });

  describe("ECharts 配置映射", () => {
    it("订单趋势：横轴日期、纵轴订单数", () => {
      const option = buildOrderTrendOption([
        { date: "2026-11-09", order_count: 96 },
        { date: "2026-11-10", order_count: 128 }
      ]) as any;

      expect(option.xAxis.data).toEqual(["2026-11-09", "2026-11-10"]);
      expect(option.series[0].type).toBe("line");
      expect(option.series[0].data).toEqual([96, 128]);
      expect(option.series[0].name).toBe("订单数");
    });

    it("状态分布：使用真实状态名称，数值为 count", () => {
      const option = buildOrderStatusOption([
        { status: 20, count: 140 },
        { status: 70, count: 20 }
      ]) as any;

      expect(option.series[0].type).toBe("pie");
      expect(option.series[0].data).toEqual([
        { name: "已支付", value: 140 },
        { name: "已退款", value: 20 }
      ]);
    });
  });

  describe("分区独立加载与失败隔离", () => {
    it("某个接口 403 时其它分区仍能正常加载", async () => {
      server.use(
        mswHttp.get("*/api/admin/dashboard/overview", () =>
          HttpResponse.json(
            { code: 1003, message: "没有访问权限", data: null },
            { status: 403 }
          )
        )
      );

      const overviewSection = useAsyncSection(getDashboardOverview);
      const trendSection = useAsyncSection(getDashboardOrderTrend);

      await Promise.allSettled([overviewSection.load(), trendSection.load()]);

      expect(overviewSection.error.value?.forbidden).toBe(true);
      expect(overviewSection.error.value?.message).toContain("权限不足");
      expect(overviewSection.data.value).toBeNull();

      expect(trendSection.error.value).toBeNull();
      expect(trendSection.data.value?.days).toHaveLength(7);
    });

    it("空数据：loaded 为 true 且数据为空，不算错误", async () => {
      server.use(
        mswHttp.get("*/api/admin/dashboard/orders/trend", () =>
          HttpResponse.json({ code: 0, message: "OK", data: { days: [] } })
        )
      );

      const section = useAsyncSection(getDashboardOrderTrend);
      await section.load();

      expect(section.error.value).toBeNull();
      expect(section.loaded.value).toBe(true);
      expect(section.data.value?.days).toEqual([]);
    });

    it("业务错误被归一化为可展示的提示", async () => {
      server.use(
        mswHttp.get("*/api/admin/dashboard/products/top", () =>
          HttpResponse.json({ code: 18002, message: "limit 非法", data: null })
        )
      );

      const section = useAsyncSection(() => getDashboardTopProducts(10));
      await section.load();

      expect(section.error.value).toEqual({
        message: "limit 非法",
        forbidden: false
      });
      expect(section.data.value).toBeNull();
    });

    it("网络异常被归一化为可读文案", async () => {
      server.use(
        mswHttp.get("*/api/admin/dashboard/flash-sales", () =>
          HttpResponse.error()
        )
      );

      const section = useAsyncSection(getDashboardFlashSales);
      await section.load();

      expect(section.error.value?.forbidden).toBe(false);
      expect(section.error.value?.message).toContain("网络异常");
    });

    it("重试成功后错误状态被清除", async () => {
      server.use(
        mswHttp.get("*/api/admin/dashboard/flash-sales", () =>
          HttpResponse.json(
            { code: 1003, message: "没有访问权限", data: null },
            { status: 403 }
          )
        )
      );

      const section = useAsyncSection(getDashboardFlashSales);
      await section.load();
      expect(section.error.value?.forbidden).toBe(true);

      // 覆盖回默认（成功）handler
      server.resetHandlers();
      await section.load();

      expect(section.error.value).toBeNull();
      expect(section.data.value?.activity_total).toBe(12);
    });
  });
});
