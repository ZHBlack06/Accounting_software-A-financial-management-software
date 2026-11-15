package http

import (
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/ids"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// MasterHandler categories/tags/merchants/regions 处理器。
type MasterHandler struct {
	s      *store.Store
	master *store.MasterRepo
}

// NewMasterHandler 构造。
func NewMasterHandler(s *store.Store, master *store.MasterRepo) *MasterHandler {
	return &MasterHandler{s: s, master: master}
}

func isUniqueViolation(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "UNIQUE constraint failed") ||
		strings.Contains(err.Error(), "constraint failed"))
}

// ---------------- categories ----------------

// ListCategories GET /api/categories?type=&include_archived=&tree=
func (h *MasterHandler) ListCategories(c *gin.Context) {
	cats, err := h.master.ListCategories(c.Query("type"), c.Query("include_archived") == "true")
	if err != nil {
		Err(c, err)
		return
	}
	dtos := make([]CategoryDTO, 0, len(cats))
	if c.Query("tree") == "true" {
		// 两级嵌套：一级为根，二级挂到 children
		var roots []CategoryDTO
		childrenOf := map[string][]CategoryDTO{}
		for i := range cats {
			d := toCategoryDTO(&cats[i])
			if d.ParentID != nil {
				childrenOf[*d.ParentID] = append(childrenOf[*d.ParentID], d)
			} else {
				roots = append(roots, d)
			}
		}
		for i := range roots {
			if ch, ok := childrenOf[roots[i].ID]; ok {
				roots[i].Children = ch
			}
		}
		if roots == nil {
			roots = []CategoryDTO{}
		}
		OK(c, gin.H{"total": len(roots), "page": 1, "page_size": len(roots), "items": roots})
		return
	}
	for i := range cats {
		dtos = append(dtos, toCategoryDTO(&cats[i]))
	}
	OK(c, gin.H{"total": len(dtos), "page": 1, "page_size": len(dtos), "items": dtos})
}

type categoryUpsertBody struct {
	Name            string  `json:"name"`
	ParentID        *string `json:"parent_id"`
	TransactionType string  `json:"transaction_type"`
	Color           string  `json:"color"`
	Icon            string  `json:"icon"`
	SortOrder       *int    `json:"sort_order"`
	IsArchived      *int    `json:"is_archived"`
}

// CreateCategory POST /api/categories。
func (h *MasterHandler) CreateCategory(c *gin.Context) {
	var b categoryUpsertBody
	if err := c.ShouldBindJSON(&b); err != nil || b.Name == "" {
		BadRequest(c, "name 必填")
		return
	}
	now := timeutil.NowUnix()
	cat := store.Category{
		ID: ids.ShortID("cat_"), Name: b.Name, Level: 1,
		TransactionType: nnStr(b.TransactionType, "expense"),
		Color: b.Color, Icon: b.Icon, CreatedAt: now, UpdatedAt: now,
	}
	if b.ParentID != nil && *b.ParentID != "" {
		parent, err := h.master.GetCategory(*b.ParentID)
		if err != nil {
			NotFound(c, "父分类不存在")
			return
		}
		if parent.Level != 1 {
			BadRequest(c, "只支持两级分类")
			return
		}
		// 二级只能挂在同 transaction_type 的一级下
		if parent.TransactionType != "both" {
			cat.TransactionType = parent.TransactionType
		}
		cat.ParentID = b.ParentID
		cat.Level = 2
	} else if cat.TransactionType != "expense" && cat.TransactionType != "income" && cat.TransactionType != "both" {
		BadRequest(c, "transaction_type 必须为 expense|income|both")
		return
	}
	if b.SortOrder != nil {
		cat.SortOrder = *b.SortOrder
	}
	if err := h.s.G.Create(&cat).Error; err != nil {
		if isUniqueViolation(err) {
			ErrCode(c, apierr.CodeDuplicate, "同级下已存在同名分类", nil)
			return
		}
		Err(c, err)
		return
	}
	Created(c, toCategoryDTO(&cat))
}

// UpdateCategory PUT /api/categories/:id。
func (h *MasterHandler) UpdateCategory(c *gin.Context) {
	cat, err := h.master.GetCategory(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "分类不存在")
			return
		}
		Err(c, err)
		return
	}
	var b categoryUpsertBody
	if err := c.ShouldBindJSON(&b); err != nil {
		BadRequest(c, "请求体非法")
		return
	}
	if b.Name != "" {
		cat.Name = b.Name
	}
	cat.Color = b.Color
	cat.Icon = b.Icon
	if b.SortOrder != nil {
		cat.SortOrder = *b.SortOrder
	}
	if b.IsArchived != nil {
		cat.IsArchived = *b.IsArchived
	}
	if err := h.s.G.Save(cat).Error; err != nil {
		if isUniqueViolation(err) {
			ErrCode(c, apierr.CodeDuplicate, "同级下已存在同名分类", nil)
			return
		}
		Err(c, err)
		return
	}
	OK(c, toCategoryDTO(cat))
}

// DeleteCategory DELETE /api/categories/:id（被引用 → 409 CATEGORY_IN_USE）。
func (h *MasterHandler) DeleteCategory(c *gin.Context) {
	cat, err := h.master.GetCategory(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "分类不存在")
			return
		}
		Err(c, err)
		return
	}
	inUse, err := h.master.CategoryInUse(cat.ID)
	if err != nil {
		Err(c, err)
		return
	}
	if inUse {
		ErrCode(c, apierr.CodeCategoryInUse, "该分类已被交易引用或存在子分类，无法删除，可改为归档", nil)
		return
	}
	if err := h.s.G.Delete(&store.Category{}, "id = ?", cat.ID).Error; err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"ok": true})
}

// ---------------- tags ----------------

// ListTags GET /api/tags?include_archived=&q=
func (h *MasterHandler) ListTags(c *gin.Context) {
	tags, err := h.master.ListTags(c.Query("include_archived") == "true", c.Query("q"))
	if err != nil {
		Err(c, err)
		return
	}
	items := make([]TagRespDTO, 0, len(tags))
	for _, t := range tags {
		items = append(items, TagRespDTO{
			ID: t.ID, Name: t.Name, Color: t.Color, UseCount: t.UseCount,
			SortOrder: t.SortOrder, IsArchived: t.IsArchived,
		})
	}
	OK(c, gin.H{"total": len(items), "page": 1, "page_size": len(items), "items": items})
}

type tagUpsertBody struct {
	Name       string `json:"name"`
	Color      string `json:"color"`
	SortOrder  *int   `json:"sort_order"`
	IsArchived *int   `json:"is_archived"`
}

// CreateTag POST /api/tags（重名 409 DUPLICATE）。
func (h *MasterHandler) CreateTag(c *gin.Context) {
	var b tagUpsertBody
	if err := c.ShouldBindJSON(&b); err != nil || b.Name == "" {
		BadRequest(c, "name 必填")
		return
	}
	now := timeutil.NowUnix()
	tag := store.Tag{ID: ids.ShortID("tag_"), Name: b.Name, Color: b.Color,
		CreatedAt: now, UpdatedAt: now}
	if b.SortOrder != nil {
		tag.SortOrder = *b.SortOrder
	}
	if err := h.s.G.Create(&tag).Error; err != nil {
		if isUniqueViolation(err) {
			ErrCode(c, apierr.CodeDuplicate, "已存在同名标签", nil)
			return
		}
		Err(c, err)
		return
	}
	Created(c, TagRespDTO{ID: tag.ID, Name: tag.Name, Color: tag.Color, SortOrder: tag.SortOrder})
}

// UpdateTag PUT /api/tags/:id。
func (h *MasterHandler) UpdateTag(c *gin.Context) {
	tag, err := h.master.GetTag(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "标签不存在")
			return
		}
		Err(c, err)
		return
	}
	var b tagUpsertBody
	if err := c.ShouldBindJSON(&b); err != nil {
		BadRequest(c, "请求体非法")
		return
	}
	if b.Name != "" {
		tag.Name = b.Name
	}
	tag.Color = b.Color
	if b.SortOrder != nil {
		tag.SortOrder = *b.SortOrder
	}
	if b.IsArchived != nil {
		tag.IsArchived = *b.IsArchived
	}
	if err := h.s.G.Save(tag).Error; err != nil {
		if isUniqueViolation(err) {
			ErrCode(c, apierr.CodeDuplicate, "已存在同名标签", nil)
			return
		}
		Err(c, err)
		return
	}
	OK(c, TagRespDTO{ID: tag.ID, Name: tag.Name, Color: tag.Color, UseCount: tag.UseCount,
		SortOrder: tag.SortOrder, IsArchived: tag.IsArchived})
}

// DeleteTag DELETE /api/tags/:id（引用中 → 409）。
func (h *MasterHandler) DeleteTag(c *gin.Context) {
	tag, err := h.master.GetTag(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "标签不存在")
			return
		}
		Err(c, err)
		return
	}
	inUse, err := h.master.TagInUse(tag.ID)
	if err != nil {
		Err(c, err)
		return
	}
	if inUse {
		ErrCode(c, apierr.CodeInUse, "该标签已被交易引用，无法删除，可改为归档", nil)
		return
	}
	if err := h.s.G.Delete(&store.Tag{}, "id = ?", tag.ID).Error; err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"ok": true})
}

// ---------------- merchants ----------------

// ListMerchants GET /api/merchants?q=&type=&limit=20
func (h *MasterHandler) ListMerchants(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 {
			limit = n
		}
	}
	list, err := h.master.ListMerchants(c.Query("q"), c.Query("type"), limit)
	if err != nil {
		Err(c, err)
		return
	}
	items := make([]MerchantDTO, 0, len(list))
	for _, m := range list {
		items = append(items, toMerchantDTO(&m))
	}
	OK(c, gin.H{"total": len(items), "page": 1, "page_size": len(items), "items": items})
}

func toMerchantDTO(m *store.Merchant) MerchantDTO {
	regionID := ""
	if m.RegionID != nil {
		regionID = *m.RegionID
	}
	return MerchantDTO{
		ID: m.ID, Name: m.Name, Alias: m.Alias, MerchantType: m.MerchantType,
		PlatformCode: m.PlatformCode, RegionID: regionID, UseCount: m.UseCount,
		IsSystem: m.IsSystem, IsArchived: m.IsArchived,
	}
}

type merchantUpsertBody struct {
	Name         string  `json:"name"`
	Alias        string  `json:"alias"`
	MerchantType string  `json:"merchant_type"`
	PlatformCode string  `json:"platform_code"`
	RegionID     *string `json:"region_id"`
	IsArchived   *int    `json:"is_archived"`
}

// CreateMerchant POST /api/merchants。
func (h *MasterHandler) CreateMerchant(c *gin.Context) {
	var b merchantUpsertBody
	if err := c.ShouldBindJSON(&b); err != nil || b.Name == "" {
		BadRequest(c, "name 必填")
		return
	}
	mt := nnStr(b.MerchantType, "offline")
	if mt != "offline" && mt != "online" {
		BadRequest(c, "merchant_type 必须为 offline|online")
		return
	}
	now := timeutil.NowUnix()
	m := store.Merchant{ID: ids.ShortID("mch_"), Name: b.Name, Alias: b.Alias,
		MerchantType: mt, PlatformCode: b.PlatformCode, RegionID: b.RegionID,
		CreatedAt: now, UpdatedAt: now}
	if err := h.s.G.Create(&m).Error; err != nil {
		if isUniqueViolation(err) {
			ErrCode(c, apierr.CodeDuplicate, "已存在同名商户", nil)
			return
		}
		Err(c, err)
		return
	}
	Created(c, toMerchantDTO(&m))
}

// UpdateMerchant PUT /api/merchants/:id。
func (h *MasterHandler) UpdateMerchant(c *gin.Context) {
	m, err := h.master.GetMerchant(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "商户不存在")
			return
		}
		Err(c, err)
		return
	}
	var b merchantUpsertBody
	if err := c.ShouldBindJSON(&b); err != nil {
		BadRequest(c, "请求体非法")
		return
	}
	if b.Name != "" {
		m.Name = b.Name
	}
	m.Alias = b.Alias
	if b.MerchantType != "" {
		if b.MerchantType != "offline" && b.MerchantType != "online" {
			BadRequest(c, "merchant_type 必须为 offline|online")
			return
		}
		m.MerchantType = b.MerchantType
	}
	m.PlatformCode = b.PlatformCode
	if b.RegionID != nil {
		m.RegionID = b.RegionID
	}
	if b.IsArchived != nil {
		m.IsArchived = *b.IsArchived
	}
	if err := h.s.G.Save(m).Error; err != nil {
		if isUniqueViolation(err) {
			ErrCode(c, apierr.CodeDuplicate, "已存在同名商户", nil)
			return
		}
		Err(c, err)
		return
	}
	OK(c, toMerchantDTO(m))
}

// DeleteMerchant DELETE /api/merchants/:id（引用中 → 409，建议归档）。
func (h *MasterHandler) DeleteMerchant(c *gin.Context) {
	m, err := h.master.GetMerchant(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "商户不存在")
			return
		}
		Err(c, err)
		return
	}
	inUse, err := h.master.MerchantInUse(m.ID)
	if err != nil {
		Err(c, err)
		return
	}
	if inUse {
		ErrCode(c, apierr.CodeInUse, "该商户已被交易引用，无法删除，可改为归档", nil)
		return
	}
	if err := h.s.G.Delete(&store.Merchant{}, "id = ?", m.ID).Error; err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"ok": true})
}

// ---------------- regions ----------------

// SearchRegions GET /api/regions/search?q=&limit=20
func (h *MasterHandler) SearchRegions(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 {
			limit = n
		}
	}
	list, err := h.master.SearchRegions(c.Query("q"), limit)
	if err != nil {
		Err(c, err)
		return
	}
	items := make([]RegionDTO, 0, len(list))
	for _, r := range list {
		items = append(items, RegionDTO{
			Code: r.Code, Name: r.Name, FullName: r.FullName, ParentCode: r.ParentCode,
			Level: r.Level, Pinyin: r.Pinyin, PinyinAbbr: r.PinyinAbbr,
		})
	}
	OK(c, gin.H{"total": len(items), "page": 1, "page_size": len(items), "items": items})
}

// GetRegion GET /api/regions/:code
func (h *MasterHandler) GetRegion(c *gin.Context) {
	r, err := h.master.GetRegion(c.Param("code"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "地区不存在")
			return
		}
		Err(c, err)
		return
	}
	OK(c, RegionDTO{Code: r.Code, Name: r.Name, FullName: r.FullName, ParentCode: r.ParentCode,
		Level: r.Level, Pinyin: r.Pinyin, PinyinAbbr: r.PinyinAbbr})
}

func parseInt(s string) (int, error) {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, apierr.New(apierr.CodeValidation, "非数字")
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}
