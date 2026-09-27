<template>
  <div class="product-main">
    <!-- <el-collapse class="collapse" v-model="activeNames"> -->
      <!-- <el-collapse-item title="商品查询" name="1"> -->
        <el-form
          label-width="80px"
          :inline="false"
          class="search"
          size="normal"
        >
          <el-row :gutter="10">
            <el-col class="searchItem" :span="6" :offset="0">
              <el-form-item label="姓名" size="mini">
                <el-input
                  v-model="name"
                  class="search-input"
                  size="mini"
                  placeholder="姓名"
                />
              </el-form-item>
            </el-col>
            <el-col class="searchItem" :span="6" :offset="0">
                <el-form-item label="电话" size="mini">
                  <el-input
                    v-model="phone"
                    class="search-input"
                    size="mini"
                    placeholder="电话"
                  />
                </el-form-item>
              </el-col>
            <!-- <el-col class="searchItem" :span="6" :offset="0">
              <el-form-item label="上架状态" size="mini">
                <el-select
                  v-model="search.publishStatus"
                  class="search-input"
                  size="mini"
                  clearable
                  placeholder="上架状态"
                >
                  <el-option label="上架" value="1" />
                  <el-option label="未上架" value="0" />
                </el-select>
              </el-form-item>
            </el-col> -->
            <el-col style="float: right" :span="4" :offset="0">
              <el-button type="primary" size="mini" @click="doSearch"
                >搜索</el-button
              >
              <el-button type="default" size="mini" @click="doReset"
                >重置
              </el-button>
            </el-col>
          </el-row>
        </el-form>
      <!-- </el-collapse-item> -->
    <!-- </el-collapse> -->

    <!-- 商品列表 -->
    <el-card class="card" shadow="never">
      <!-- 商品列表 -->

      <!-- 商品列表 -->

      <el-table
        :data="productList"
        border
        style="width: 100%"
      >
        <el-table-column type="index" width="80" label="序号" fixed="left" />
        <el-table-column label="用户" width="120" prop="user_name">
        </el-table-column>
        <el-table-column label="收货人" width="100" prop="name">
        </el-table-column>
        <el-table-column label="收货电话" width="130" prop="phone">
        </el-table-column>
        <el-table-column label="默认" width="80">
          <template slot-scope="scope">
            <el-tag v-if="scope.row.is_default === 1" type="success" size="mini">是</el-tag>
            <el-tag v-else type="info" size="mini">否</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="收货地址" prop="address">
        </el-table-column>
        <el-table-column fixed="right" label="操作" width="80">
          <template slot-scope="scope">
            <el-button type="text" size="small" @click="deletes(scope.row.id)">
              <span style="color: red">删除</span>
            </el-button>
          </template>
        </el-table-column>
      </el-table>
      <!-- 所有列表都要有分页 除非有特殊要求 否则数据多的情况下就没办法看了 -->
      <el-pagination
        @size-change="handleSizeChange"
        @current-change="handleCurrentChange"
        :current-page.sync="currentPage4"
        :page-sizes="[10, 20, 50, 100]"
        :page-size="limit"
        layout="total, sizes, prev, pager, next, jumper"
        :total="total">
      </el-pagination>
    </el-card>
  </div>
</template>

<script>
import { addressList, addressDelete } from '@/api/api.js'
export default {
  name: "ProductList",
  components: {},
  data() {
    return {
      name: '',
      phone: '',
      downloadLoading: false,
      activeNames: ["1"],
      keyword: '',
      loading: true, // 加载
      productList: [],
      currentPage4: 1,//当前页
      limit: 10,//每页条数
      page: 1,//页数
      total: 0//总条数
    };
  },
  created() {
    this.getList()
  },
  methods: {
    getList() {
      let params = {
        size: this.limit,
        page: this.currentPage4,
        name: this.name,
        phone: this.phone
      }
      addressList(params).then( res => {
        console.log(res)
        if(res.code === 0){
          this.productList = res.data.list
          this.total = res.data.total || res.data.count || 0
        }
      })
    },

    //分页
    handleSizeChange(val) {
      this.limit = val;
      this.currentPage4 = 1; // 重置到第一页
      this.getList(); // 获取新数据
      console.log(`每页 ${val} 条`);
    },
    handleCurrentChange(val) {
      this.currentPage4 = val;
      this.getList();
    },

    // 重置搜索栏
    doReset() {
      this.name = ''
      this.phone = ''
      this.currentPage4 = 1
      this.limit = 10
      this.getList();
    },
    // 搜索按钮
    doSearch() {
      this.getList()
    },
    // 删除地址
    deletes(id) {
      this.$confirm('删除后不可恢复, 是否继续?', '提示', {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }).then(() => {
        let params = {
          consignee_id: id
        }
        addressDelete(params).then(res => {
          if (res.code === 0) {
            this.$message({
              message: '删除成功',
              type: 'success'
            })
            this.getList()
          } else {
            this.$message({
              message: res.msg,
              type: 'error'
            })
          }
        })
      }).catch(() => {})
    },
  },
};
</script>


<style lang="scss">
.el-collapse-item__header{
    margin-left: 10px!important;
    font-size: 18px;
}
.search {
  padding: 30px 20px 10px 10px;
}
</style>
