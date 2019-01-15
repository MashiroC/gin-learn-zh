// Copyright 2013 Julien Schmidt. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// at https://github.com/julienschmidt/httprouter/blob/master/LICENSE

package gin

import (
	"net/url"
	"strings"
	"unicode"
)

// Param is a single URL parameter, consisting of a key and a value.
//参数
type Param struct {
	Key   string
	Value string
}

// Params is a Param-slice, as returned by the router.
// The slice is ordered, the first URL parameter is also the first slice value.
// It is therefore safe to read values by the index.

type Params []Param

// Get returns the value of the first Param which key matches the given name.
// If no matching Param is found, an empty string is returned.
//获得请求中的参数
func (ps Params) Get(name string) (string, bool) {
	for _, entry := range ps {
		if entry.Key == name {
			return entry.Value, true
		}
	}
	return "", false
}

// ByName returns the value of the first Param which key matches the given name.
// If no matching Param is found, an empty string is returned.
//对上述方法的包装，忽略掉是否查找到的返回值
func (ps Params) ByName(name string) (va string) {
	va, _ = ps.Get(name)
	return
}

//方法对应的路由树
type methodTree struct {
	method string //方法
	root   *node  //路由树的根节点
}

type methodTrees []methodTree

//根据方法找路由树
func (trees methodTrees) get(method string) *node {
	for _, tree := range trees {
		if tree.method == method {
			return tree.root
		}
	}
	return nil
}

//求出两个值的小值
func min(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

//计算该path下路由参数的数量
func countParams(path string) uint8 {
	var n uint
	for i := 0; i < len(path); i++ {
		if path[i] != ':' && path[i] != '*' {
			continue
		}
		n++
	}
	if n >= 255 {
		return 255
	}
	return uint8(n)
}

type nodeType uint8

const (
	static   nodeType = iota // default
	root                     // 根节点
	param                    // 参数节点
	catchAll                 // 相当于是参数节点的加强版 catchAll节点只能是叶子节点 获取指定规则后面所有的字符 我称为匹配节点
)

//路由树上的节点
type node struct {
	path      string        //节点的路由
	indices   string        //子节点快速查找字符串，由每个子节点的首字母构成，根据子节点下节点总数排序
	children  []*node       //子节点
	handlers  HandlersChain //处理该节点的方法的链
	priority  uint32        //优先级。根据节点下的节点数量赋值
	nType     nodeType      //节点类型
	maxParams uint8         //节点下路由的最大路由参数数量
	wildChild bool          //是否为一个路由参数的节点的父节点 如果是 那么该节点下一层不能有路由参数节点外其他节点存在
}

// increments priority of the given child and reorders if necessary.
//对子节点和节点的indices排序
//新的路由添加后 对子节点按照优先级 对children和indices进行排序
//优先级为该节点下的(包括该节点本身和子节点)的路由数量
//pos是新增的路由的节点下下标
//调用时如果是新添加的节点 那么新节点的path暂时为空 位于数组的最后一个元素
//如第一条路由是/helloworld 第二条是/hellogo
//n就是/hello节点 pos就是1
//如现有两条路由 /helloworld和/hellogo
//新添加一条路由 /hellowowowo
//这里会调用两次 第一次n是/hello pos是0 第二次调用n是wo pos是1
func (n *node) incrementChildPrio(pos int) int {
	n.children[pos].priority++
	prio := n.children[pos].priority

	// adjust position (move to front)
	//因为只变动了一个节点的优先级 其他节点是从大到小排序好的 使用冒泡的方法排序子节点数组
	newPos := pos
	for newPos > 0 && n.children[newPos-1].priority < prio {
		// swap node positions
		n.children[newPos-1], n.children[newPos] = n.children[newPos], n.children[newPos-1]

		newPos--
	}

	//排序好之后修改快速索引字符串
	// build new index char string
	if newPos != pos {
		n.indices = n.indices[:newPos] + // unchanged prefix, might be empty
			n.indices[pos:pos+1] + // the index char we move
			n.indices[newPos:pos] + n.indices[pos+1:] // rest without char at 'pos'
	}

	//返回排序好之后新节点的下标
	return newPos
}

// addRoute adds a node with the given handle to the path.
// Not concurrency-safe!
//添加handle到路由上 非线程安全
func (n *node) addRoute(path string, handlers HandlersChain) {
	fullPath := path
	n.priority++
	numParams := countParams(path)

	// non-empty tree
	//如果树非空
	if len(n.path) > 0 || len(n.children) > 0 {
	walk:
		//循环取非公共的节点
		for {
			// Update maxParams of the current node
			//更新节点下面的路由的最大参数数量
			if numParams > n.maxParams {
				n.maxParams = numParams
			}

			// Find the longest common prefix.
			// This also implies that the common prefix contains no ':' or '*'
			// since the existing key can't contain those chars.
			//找到公共最长前缀的下标
			i := 0
			max := min(len(path), len(n.path))
			for i < max && path[i] == n.path[i] {
				i++
			}

			//分割路由，抽取公共前缀替代这个节点，剩余部分作为一个子节点
			// Split edge
			if i < len(n.path) {
				//这个是将原来的节点抽取出前缀之后的新节点
				//比如原本的路由/helloworld 新添加的路由是/hellogo
				//这个child是world
				child := node{
					path:      n.path[i:],
					wildChild: n.wildChild,
					indices:   n.indices,
					children:  n.children,
					handlers:  n.handlers,
					priority:  n.priority - 1,
				}

				// Update maxParams (max of all children)
				//更新节点下的最大路由参数数量
				for i := range child.children {
					if child.children[i].maxParams > child.maxParams {
						child.maxParams = child.children[i].maxParams
					}
				}

				//抽离公共前缀后 创建子节点
				// 例中的world节点连接在/hello节点下
				n.children = []*node{&child}
				// []byte for proper unicode char conversion, see #65

				//将子节点的首字母放在父节点的indices下
				//上例中的'w'
				n.indices = string([]byte{n.path[i]})
				//将本节点的path改为公共前缀
				n.path = path[:i]
				n.handlers = nil
				n.wildChild = false
			}

			// Make new node a child of this node
			//如果公共前缀和新添加路由的path一样
			//抽取非公共部分 创建新节点
			if i < len(path) {
				//path非公共部分 比如新路由/hellogo的go
				path = path[i:]

				//如果本节点是一个参数节点的父节点
				//就让n指向参数节点
				if n.wildChild {
					n = n.children[0]
					n.priority++

					//如果后面还有参数节点
					// Update maxParams of the child node
					if numParams > n.maxParams {
						n.maxParams = numParams
					}
					numParams--

					// Check if the wildcard matches
					//检查路由 如果没问题则continue 有问题panic出来
					//这里有个bug
					//如果添加的两条路由为/aaa/:bbb/ccc 和 /aaa/:bbb/ddd/:eee/fff 会panic出来 反之不会
					//如果上述的路由第二条变成/aaa/:bbb/ddd/:eee/fff/:ggg/hhh 则不会panic出来
					//TODO:好像没这个问题了 好像是我的问题
					if len(path) >= len(n.path) && n.path == path[:len(n.path)] {
						// check for longer wildcard, e.g. :name and :names
						if len(n.path) >= len(path) || path[len(n.path)] == '/' {
							continue walk
						}
					}

					pathSeg := path
					if n.nType != catchAll {
						pathSeg = strings.SplitN(path, "/", 2)[0]
					}
					prefix := fullPath[:strings.Index(fullPath, pathSeg)] + n.path
					panic("'" + pathSeg +
						"' in new path '" + fullPath +
						"' conflicts with existing wildcard '" + n.path +
						"' in existing prefix '" + prefix +
						"'")
				}

				//后面没有参数节点了
				c := path[0]

				//如果本节点是参数节点
				//当一个参数节点读取到`/`证明已经取出来了一个完整的参数节点
				// slash after param
				if n.nType == param && c == '/' && len(n.children) == 1 {
					n = n.children[0]
					n.priority++
					continue walk
				}

				// Check if a child with the next path byte exists
				//如果和子节点首字母一样，那么具有公共部分，对increment和子节点重新排序 然后取子节点 继续循环
				for i := 0; i < len(n.indices); i++ {
					if c == n.indices[i] {
						i = n.incrementChildPrio(i)
						n = n.children[i]
						continue walk
					}
				}

				// Otherwise insert it
				//如果当前节点不是参数节点
				if c != ':' && c != '*' {
					// []byte for proper unicode char conversion, see #65
					//更新快速查找字符串
					n.indices += string([]byte{c})
					//新的节点
					//上例中/hellogo中的go
					child := &node{
						maxParams: numParams,
					}
					n.children = append(n.children, child)
					n.incrementChildPrio(len(n.indices) - 1)
					n = child
				}
				n.insertChild(numParams, path, fullPath, handlers)
				return

			} else if i == len(path) { // Make node a (in-path) leaf
				//向该节点添加handlesChain
				if n.handlers != nil {
					panic("handlers are already registered for path '" + fullPath + "'")
				}
				n.handlers = handlers
			}
			return
		}
	} else { // Empty tree
		//如果是一棵空的树 那么就以刚添加的节点为root节点
		n.insertChild(numParams, path, fullPath, handlers)
		n.nType = root
	}
}

//向节点插入子节点数据
//这里的n就是新节点了，而不是新节点的父节点
//就例如原本有一条/helloworld路由 新添加一条/hellogo路由
//这里的n不是/hello这个节点 而是在/hello下面新开的一个空节点
//(自我感觉这种做法有点怪)
func (n *node) insertChild(numParams uint8, path string, fullPath string, handlers HandlersChain) {
	var offset int // already handled bytes of the path

	//这个loop是解析参数路由
	// find prefix until first wildcard (beginning with ':' or '*')
	for i, max := 0, len(path); numParams > 0; i++ {
		c := path[i]
		if c != ':' && c != '*' {
			continue
		}

		// find wildcard end (either '/' or path end)
		//end是参数节点的节点名字最后一个字母下标 比如:name中的e的下标
		//如果当前参数节点:或*存在两个或以上 不符合路由规则 panic出来
		end := i + 1
		for end < max && path[end] != '/' {
			switch path[end] {
			// the wildcard name must not contain ':' and '*'
			case ':', '*':
				panic("only one wildcard per path segment is allowed, has: '" +
					path[i:] + "' in path '" + fullPath + "'")
			default:
				end++
			}
		}

		// check if this Node existing children which would be
		// unreachable if we insert the wildcard here
		//因为当前节点n是新插入的节点，所以n下面不应该有子节点
		if len(n.children) > 0 {
			panic("wildcard route '" + path[i:end] +
				"' conflicts with existing children in path '" + fullPath + "'")
		}

		// check if the wildcard has a name
		//路由规则不应有一个或零个字母表示的参数节点，比如`:`或`:a` 如果有 panic出来
		if end-i < 2 {
			panic("wildcards must be named with a non-empty name in path '" + fullPath + "'")
		}

		//如果当前是参数节点
		if c == ':' { // param
			// split path at the beginning of the wildcard
			//拿到参数节点之前的uri 赋给当前节点
			if i > 0 {
				n.path = path[offset:i]
				offset = i
			}

			child := &node{
				nType:     param,
				maxParams: numParams,
			}
			n.children = []*node{child}
			n.wildChild = true
			//现在是子节点 也就是该给参数节点赋值了
			n = child
			n.priority++
			numParams--

			// if the path doesn't end with the wildcard, then there
			// will be another non-wildcard subpath starting with '/'
			//如果后面还有节点 end==max代表参数节点后面没有新的节点了
			if end < max {
				//给当前的参数节点赋值
				n.path = path[offset:end]
				offset = end

				child := &node{
					maxParams: numParams,
					priority:  1,
				}
				n.children = []*node{child}
				//现在n是参数节点的子节点 也就是应该给`/welcome/:name/hello`中的`/hello`节点赋值了
				n = child
			}

		} else { // catchAll
			//匹配节点
			//判断匹配节点是不是在uri的最后 匹配节点只能在uri的最后
			if end != max || numParams > 1 {
				panic("catch-all routes are only allowed at the end of the path in path '" + fullPath + "'")
			}

			//匹配节点不能在中间 TODO:没太看懂这个
			if len(n.path) > 0 && n.path[len(n.path)-1] == '/' {
				panic("catch-all conflicts with existing handle for the path segment root in path '" + fullPath + "'")
			}

			// currently fixed width 1 for '/'
			//匹配节点的上一个字母只能是`/`
			i--
			if path[i] != '/' {
				panic("no / before catch-all in path '" + fullPath + "'")
			}

			//抽取出匹配节点前的uri 给当前节点赋值
			//比如`/welcome/*name`中的`/welcome/`
			n.path = path[offset:i]

			//一个匹配节点需要在tree上开两个节点
			//第一个节点拥有一个空的path 它只有一个子节点 就是第二个节点 子节点的path是匹配节点的*key

			// first node: catchAll node with empty path
			//第一个节点
			child := &node{
				wildChild: true,
				nType:     catchAll,
				maxParams: 1,
			}
			n.children = []*node{child}
			n.indices = string(path[i])
			n = child
			n.priority++

			// second node: node holding the variable
			//第二个节点 也是第一个的子节点
			child = &node{
				path:      path[i:],
				nType:     catchAll,
				maxParams: 1,
				handlers:  handlers,
				priority:  1,
			}
			n.children = []*node{child}

			//匹配节点后面不再有未处理的path
			return
		}
	}

	// insert remaining path part and handle to the leaf
	//给参数路由最后剩下的部分赋值
	//比如`/welcome/:name`中的`:name` 或`/welcome/:name/hello`中的`/hello`
	n.path = path[offset:]
	n.handlers = handlers
}

// getValue returns the handle registered with the given path (key). The values of
// wildcards are saved to a map.
// If no handle can be found, a TSR (trailing slash redirect) recommendation is
// made if a handle exists with an extra (without the) trailing slash for the
// given path.
//根据uri和一些参数得到handles
//tsr是树上的path是否以`/`结尾
//unescape是是否需要QueryEscape解码
func (n *node) getValue(path string, po Params, unescape bool) (handlers HandlersChain, p Params, tsr bool) {
	p = po
walk: // Outer loop for walking the tree
	for {
		// 如果当前节点path比path短 证明还要继续循环去找
		// 这里的算法有点奇怪，满足当前节点的path是请求的path的一部分
		// 就切割出后面的部分，然后去快速索引字符找首字母一样的
		// 找到后就提取那个节点 继续循环
		if len(path) > len(n.path) {
			//如果path符合当前节点的path
			//那么切割出后面的部分
			if path[:len(n.path)] == n.path {
				path = path[len(n.path):]
				// If this node does not have a wildcard (param or catchAll)
				// child,  we can just look up the next child node and continue
				// to walk down the tree
				// 如果当前节点不是参数节点的父节点 根据快速索引字符串 找到首字母相同的节点
				// 然后n=那个节点 continue
				if !n.wildChild {
					c := path[0]
					for i := 0; i < len(n.indices); i++ {
						if c == n.indices[i] {
							n = n.children[i]
							continue walk
						}
					}

					// Nothing found.
					// We can recommend to redirect to the same URL without a
					// trailing slash if a leaf exists for that path.
					// 没找到节点 404或405
					tsr = path == "/" && n.handlers != nil
					return
				}

				// handle wildcard child
				//下一个节点是参数节点或匹配节点
				//现在n是参数节点或匹配节点了
				n = n.children[0]
				switch n.nType {
				//参数节点
				case param:
					// find param end (either '/' or path end)
					//提取参数节点名字
					end := 0
					for end < len(path) && path[end] != '/' {
						end++
					}

					// save param value
					//如果参数的空间大小比参数节点数量小，就再分配资源
					if cap(p) < int(n.maxParams) {
						p = make(Params, 0, n.maxParams)
					}
					//得到参数，赋值给返回的param
					i := len(p)
					p = p[:i+1] // expand slice within preallocated capacity
					p[i].Key = n.path[1:]
					val := path[:end]
					//如果是QueryEscape 就解码 反之不用 直接赋值
					if unescape {
						var err error
						if p[i].Value, err = url.QueryUnescape(val); err != nil {
							p[i].Value = val // fallback, in case of error
						}
					} else {
						p[i].Value = val
					}

					// we need to go deeper!
					//(吐槽一下这个注释好萌)
					//参数节点后面还有path没有解析
					if end < len(path) {
						//如果有子节点
						//因为参数节点后面跟的必定是一个`/`节点 所以直接取children[0]
						if len(n.children) > 0 {
							path = path[end:]
							n = n.children[0]
							continue walk
						}

						// ... but we can't
						// 没有子节点了 证明 404 or 405 了
						tsr = len(path) == end+1
						return
					}

					//参数节点后面没有path可解析了
					if handlers = n.handlers; handlers != nil {
						return
					}

					//该参数节点没有handle 如果参数节点有子节点 那么必定是`/` 就构成了tsr
					if len(n.children) == 1 {
						// No handle found. Check if a handle for this path + a
						// trailing slash exists for TSR recommendation
						n = n.children[0]
						tsr = n.path == "/" && n.handlers != nil
					}

					return

				case catchAll:
					//如果是匹配节点
					// save param value
					if cap(p) < int(n.maxParams) {
						p = make(Params, 0, n.maxParams)
					}
					i := len(p)
					p = p[:i+1] // expand slice within preallocated capacity
					p[i].Key = n.path[2:] // 对于一个匹配节点 key应该为`/*`之后的所有内容 value是path后面所有字符
					if unescape {
						var err error
						if p[i].Value, err = url.QueryUnescape(path); err != nil {
							p[i].Value = path // fallback, in case of error
						}
					} else {
						p[i].Value = path
					}

					handlers = n.handlers
					return

				default:
					panic("invalid node type")
				}
			}
		} else if path == n.path {
			// We should have reached the node containing the handle.
			// Check if this node has a handle registered.
			//如果path一样了，那么证明当前节点就是要寻找的节点
			if handlers = n.handlers; handlers != nil {
				return
			}

			if path == "/" && n.wildChild && n.nType != root {
				tsr = true
				return
			}

			// No handle found. Check if a handle for this path + a
			// trailing slash exists for trailing slash recommendation
			//如果当前节点的handles是空的，看看子节点有没有`/` 如果有 证明tsr了
			for i := 0; i < len(n.indices); i++ {
				if n.indices[i] == '/' {
					n = n.children[i]
					tsr = (len(n.path) == 1 && n.handlers != nil) ||
						(n.nType == catchAll && n.children[0].handlers != nil)
					return
				}
			}

			return
		}

		// Nothing found. We can recommend to redirect to the same URL with an
		// extra trailing slash if a leaf exists for that path
		//这个相当于是判断该节点的path是不是path+'/'
		tsr = (path == "/") ||
			(len(n.path) == len(path)+1 && n.path[len(path)] == '/' &&
				path == n.path[:len(n.path)-1] && n.handlers != nil)
		return
	}
}

// findCaseInsensitivePath makes a case-insensitive lookup of the given path and tries to find a handler.
// It can optionally also fix trailing slashes.
// It returns the case-corrected path and a bool indicating whether the lookup
// was successful.
//对给定的path不分大小写以及忽略末尾斜杠进行查找
//如果tsr(尾斜杠)了就返回一个修复的路径
func (n *node) findCaseInsensitivePath(path string, fixTrailingSlash bool) (ciPath []byte, found bool) {
	ciPath = make([]byte, 0, len(path)+1) // preallocate enough memory

	// Outer loop for walking the tree
	for len(path) >= len(n.path) && strings.ToLower(path[:len(n.path)]) == strings.ToLower(n.path) {
		path = path[len(n.path):]
		ciPath = append(ciPath, n.path...)

		if len(path) > 0 {
			// If this node does not have a wildcard (param or catchAll) child,
			// we can just look up the next child node and continue to walk down
			// the tree
			if !n.wildChild {
				r := unicode.ToLower(rune(path[0]))
				for i, index := range n.indices {
					// must use recursive approach since both index and
					// ToLower(index) could exist. We must check both.
					if r == unicode.ToLower(index) {
						out, found := n.children[i].findCaseInsensitivePath(path, fixTrailingSlash)
						if found {
							return append(ciPath, out...), true
						}
					}
				}

				// Nothing found. We can recommend to redirect to the same URL
				// without a trailing slash if a leaf exists for that path
				found = fixTrailingSlash && path == "/" && n.handlers != nil
				return
			}

			n = n.children[0]
			switch n.nType {
			case param:
				// find param end (either '/' or path end)
				k := 0
				for k < len(path) && path[k] != '/' {
					k++
				}

				// add param value to case insensitive path
				ciPath = append(ciPath, path[:k]...)

				// we need to go deeper!
				if k < len(path) {
					if len(n.children) > 0 {
						path = path[k:]
						n = n.children[0]
						continue
					}

					// ... but we can't
					if fixTrailingSlash && len(path) == k+1 {
						return ciPath, true
					}
					return
				}

				if n.handlers != nil {
					return ciPath, true
				} else if fixTrailingSlash && len(n.children) == 1 {
					// No handle found. Check if a handle for this path + a
					// trailing slash exists
					n = n.children[0]
					if n.path == "/" && n.handlers != nil {
						return append(ciPath, '/'), true
					}
				}
				return

			case catchAll:
				return append(ciPath, path...), true

			default:
				panic("invalid node type")
			}
		} else {
			// We should have reached the node containing the handle.
			// Check if this node has a handle registered.
			if n.handlers != nil {
				return ciPath, true
			}

			// No handle found.
			// Try to fix the path by adding a trailing slash
			if fixTrailingSlash {
				for i := 0; i < len(n.indices); i++ {
					if n.indices[i] == '/' {
						n = n.children[i]
						if (len(n.path) == 1 && n.handlers != nil) ||
							(n.nType == catchAll && n.children[0].handlers != nil) {
							return append(ciPath, '/'), true
						}
						return
					}
				}
			}
			return
		}
	}

	// Nothing found.
	// Try to fix the path by adding / removing a trailing slash
	if fixTrailingSlash {
		if path == "/" {
			return ciPath, true
		}
		if len(path)+1 == len(n.path) && n.path[len(path)] == '/' &&
			strings.ToLower(path) == strings.ToLower(n.path[:len(path)]) &&
			n.handlers != nil {
			return append(ciPath, n.path...), true
		}
	}
	return
}
