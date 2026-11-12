import type { EChartsCoreOption } from "echarts/core";
import type {
  DashboardStatusItem,
  DashboardTrendItem
} from "@/api/modules/dashboard";
import { formatOrderStatus } from "@/constants/order-status";

/**
 * 图表配置构建（纯函数）。
 *
 * 抽成纯函数的原因：ECharts 渲染依赖 DOM，在 jsdom 单测中成本高且脆弱；
 * 而"接口数据 → 图表配置"的映射才是真正的业务逻辑，适合直接断言。
 */

/** 订单趋势折线图：横轴日期、纵轴订单数 */
export function buildOrderTrendOption(
  days: DashboardTrendItem[],
  dark = false
): EChartsCoreOption {
  return {
    color: [dark ? "#9ab8c8" : "#4b6878"],
    textStyle: {
      fontFamily: "sans-serif",
      color: dark ? "#b0b2b3" : "#62656a"
    },
    grid: { left: 8, right: 20, top: 28, bottom: 8, containLabel: true },
    tooltip: {
      backgroundColor: dark ? "#202326" : "#ffffff",
      borderColor: dark ? "#3b3e40" : "#dddeda",
      textStyle: { color: dark ? "#f0f0ed" : "#17191d" },
      trigger: "axis",
      axisPointer: { type: "line" }
    },
    xAxis: {
      type: "category",
      axisLabel: { color: dark ? "#b0b2b3" : "#62656a" },
      boundaryGap: false,
      data: days.map(item => item.date),
      axisTick: { show: false },
      axisLine: { lineStyle: { color: dark ? "#4b4e50" : "#dddeda" } }
    },
    yAxis: {
      type: "value",
      // 订单数只能是整数，避免出现 0.5 这类刻度
      minInterval: 1,
      axisLabel: { color: dark ? "#b0b2b3" : "#62656a" },
      splitLine: {
        lineStyle: { type: "dashed", color: dark ? "#34373a" : "#e8e9e4" }
      }
    },
    series: [
      {
        name: "订单数",
        type: "line",
        smooth: true,
        symbol: "circle",
        symbolSize: 6,
        showSymbol: days.length <= 14,
        data: days.map(item => item.order_count),
        areaStyle: { opacity: 0.12 },
        lineStyle: { width: 2 }
      }
    ]
  };
}

/**
 * 订单状态分布环形图。
 *
 * 状态名来自 `@/constants/order-status`（依据后端订单状态机常量），
 * 未登记的状态码会显示为 `状态 <code>`，不做语义猜测。
 */
export function buildOrderStatusOption(
  items: DashboardStatusItem[],
  dark = false
): EChartsCoreOption {
  return {
    color: ["#617d8c", "#a38b60", "#47745e", "#958897", "#b96d62", "#89928a"],
    textStyle: {
      fontFamily: "sans-serif",
      color: dark ? "#b0b2b3" : "#62656a"
    },
    tooltip: {
      backgroundColor: dark ? "#202326" : "#ffffff",
      borderColor: dark ? "#3b3e40" : "#dddeda",
      textStyle: { color: dark ? "#f0f0ed" : "#17191d" },
      trigger: "item",
      formatter: "{b}: {c} ({d}%)"
    },
    legend: {
      type: "scroll",
      bottom: 0,
      textStyle: { color: dark ? "#b0b2b3" : "#62656a" },
      itemWidth: 10,
      itemHeight: 10
    },
    series: [
      {
        name: "订单状态",
        type: "pie",
        radius: ["45%", "68%"],
        center: ["50%", "45%"],
        avoidLabelOverlap: true,
        itemStyle: {
          borderRadius: 4,
          borderColor: dark ? "#202326" : "#fff",
          borderWidth: 1
        },
        label: { formatter: "{b}\n{c}", color: dark ? "#b0b2b3" : "#62656a" },
        labelLine: { length: 8, length2: 8 },
        data: items.map(item => ({
          name: formatOrderStatus(item.status),
          value: item.count
        }))
      }
    ]
  };
}
