import { login, logout, getInfo } from "@/api/user";
import { getToken, setToken, removeToken } from "@/utils/auth";
import router, { resetRouter } from "@/router";

const state = {
  token: getToken(),
  name: "",
  avatar: "",
  introduction: "",
  roles: [],
};

const mutations = {
  SET_TOKEN: (state, token) => {
    state.token = token;
  },
  SET_INTRODUCTION: (state, introduction) => {
    state.introduction = introduction;
  },
  SET_NAME: (state, name) => {
    state.name = name;
  },
  SET_AVATAR: (state, avatar) => {
    state.avatar = avatar;
  },
  SET_ROLES: (state, roles) => {
    state.roles = roles;
  },
};

const actions = {
  // user login
  //登录接口
  login({ commit }, userInfo) {
    const { name, password } = userInfo;
    return new Promise((resolve, reject) => {
      //trim是防止用户输入空格
      login({ name: name.trim(), password: password })
        .then((response) => {
          if (response.code === 0) {
            // JWT token格式
            const token = "Bearer " + response.data.token;
            const userInfo = {
              ...response.data,
              tokens: token
            };
            localStorage.setItem("info", JSON.stringify(userInfo));
            
            //这里是把token存在了vuex和cookie里面
            commit("SET_TOKEN", token);
            setToken(token);
            console.log("登录成功:", userInfo);
            resolve(response);
          } else {
            reject(new Error(response.msg || "登录失败"));
          }
        })
        .catch((error) => {
          reject(error);
        });
    });
  },

  // get user info
  //获取用户信息
  getInfo({ commit, state }) {
    return new Promise((resolve, reject) => {
      const info = JSON.parse(localStorage.getItem("info"));
      console.log("info", info);
      
      if (!info) {
        reject("Verification failed, please Login again.");
        return;
      }
      
      // 简化用户信息，基于JWT响应
      const data = {
        roles: ["*"], // 暂时给管理员所有权限
        introduction: "Administrator", 
        avatar: "https://wpimg.wallstcn.com/f778738c-e4f8-4870-b634-56703b4acafe.gif",
        name: "test",
        token: info.tokens,
      };
      
      const { roles, name, avatar, introduction, token } = data;
      if (!roles || roles.length <= 0) {
        reject("getInfo: roles must be a non-null array!");
        return;
      }

      commit("SET_ROLES", roles);
      commit("SET_NAME", name);
      commit("SET_AVATAR", avatar);
      commit("SET_INTRODUCTION", introduction);
      commit("SET_TOKEN", token);
      resolve(data);
    });
  },

  // user logout
  logout({ commit, state, dispatch }) {
    return new Promise((resolve, reject) => {
      // 先清除本地状态，避免退出时其他请求报权限不足
      commit("SET_TOKEN", "");
      commit("SET_ROLES", []);
      removeToken();
      resetRouter();
      localStorage.removeItem("info");

      // reset visited views and cached views
      dispatch("tagsView/delAllViews", null, { root: true });

      // 后端登出（忽略错误）
      logout({ token: state.token }).catch(() => {});
      
      resolve();
    });
  },

  // remove token
  resetToken({ commit }) {
    return new Promise((resolve) => {
      commit("SET_TOKEN", "");
      commit("SET_ROLES", []);
      removeToken();
      resolve();
    });
  },

  // dynamically modify permissions
  async changeRoles({ commit, dispatch }, role) {
    const token = role + "-token";

    commit("SET_TOKEN", token);
    setToken(token);

    const { roles } = await dispatch("getInfo");

    resetRouter();

    // generate accessible routes map based on roles
    const accessRoutes = await dispatch("permission/generateRoutes", roles, {
      root: true,
    });
    // dynamically add accessible routes
    router.addRoutes(accessRoutes);

    // reset visited views and cached views
    dispatch("tagsView/delAllViews", null, { root: true });
  },
};

export default {
  namespaced: true,
  state,
  mutations,
  actions,
};
