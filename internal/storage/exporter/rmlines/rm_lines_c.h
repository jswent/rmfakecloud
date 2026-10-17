/* C declarations of the extern "C" API of LIB rMLines 1.4.7
 * (https://github.com/RedTTGMoss/librm_lines). Upstream's library.h includes
 * C++ headers, so cgo cannot use it. Prototypes copied from
 * rm_lines/headers/library.h, renderer/renderer_export.h and
 * scene_tree/scene_tree_export.h; RendererConfig mirrors
 * renderer/renderer_config.h.
 *
 * Every const char* returned by the library is a thread_local buffer that
 * the next call of the same function on the same OS thread overwrites;
 * failures return "" (never NULL). Never free them. */
#pragma once
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef void (*LogFunc)(const char *);
void setLogger(LogFunc logger);
void setErrorLogger(LogFunc errorLogger);
void setDebugLogger(LogFunc debugLogger);
void setDebugMode(bool debug);
bool getDebugMode(void);

/* scene tree */
const char *buildTree(const char *rmPath);
int destroyTree(const char *treeId);
bool convertToJsonFile(const char *treeId, const char *outPath);
const char *convertToJson(const char *treeId);
const char *getSceneInfo(const char *treeId);
const char *getImageInfo(const char *treeId);

/* renderer */
const char *makeRenderer(const char *treeId, int pageType, bool landscape); /* invalid treeId segfaults */
int destroyRenderer(const char *rendererId);
const char *getParagraphs(const char *rendererId);
const char *getAnchors(const char *rendererId);
const char *getLayers(const char *rendererId);
const char *getLayerFull(const char *rendererId, const char *stringLayerId); /* id without ':' aborts */
bool textToMdFile(const char *rendererId, const char *outputFile);
const char *textToMd(const char *rendererId);
bool textToTxtFile(const char *rendererId, const char *outputFile);
const char *textToTxt(const char *rendererId);
bool textToHtmlFile(const char *rendererId, const char *outputFile);
const char *textToHtml(const char *rendererId);
/* dataSize < width*height*4 aborts */
void getFrame(const char *rendererId, uint32_t *data, size_t dataSize,
              int x, int y, int frameWidth, int frameHeight,
              int width, int height, bool antialias);
struct RendererConfig;
struct RendererConfig *getConfig(const char *rendererId);
void setTemplate(const char *rendererId, const char *templateName);
const char *getSizeTracker(const char *rendererId, const char *layerId); /* unknown layer aborts */
void addImage(const char *rendererId, const char *uuid, const char *path);
void setBackdrop(const char *rendererId, const uint8_t *data, size_t size,
                 uint32_t width, uint32_t height, uint32_t stride); /* keeps the pointer */

/* PageType (renderer.h); stored but unused by 1.4.7. */
enum { RML_PAGE_NOTEBOOK = 0, RML_PAGE_DOCUMENT = 1 };

/* Tool ids (scene_items.h) used for the highlighter pass. */
enum { RML_TOOL_HIGHLIGHTER_1 = 5, RML_TOOL_HIGHLIGHTER_2 = 18 };

/* CrdtId: { uint8_t first; uint64_t second; } */
typedef struct {
    uint8_t first;
    uint64_t second;
} RmlCrdtId;

#define RML_CONFIG_VERSION 4
#define RML_PEN_LIST_LEN 20
#define RML_DISABLED_LAYERS_LEN 10

/* Mirror of struct RendererConfig, configVersion 4. */
struct RendererConfig {
    uint8_t configVersion; /* const in C++ */
    int8_t penWhitelist[RML_PEN_LIST_LEN];
    int8_t penBlacklist[RML_PEN_LIST_LEN]; /* -1 = unused slot */
    bool useWhitelist;
    RmlCrdtId disabledLayers[RML_DISABLED_LAYERS_LEN]; /* {0,0} = unused slot */
    bool enableText;
    bool enableImages;
    bool enableGlyphHighlights;
    bool enableBackdrop;
    bool useBackdropForSamplingOnly;
    bool followRulesInJson;
    bool strokerDataInJson;
    float backdropOffsetX;
    float backdropOffsetY;
    uint8_t backdropAlign;
};

/* An upstream ABI change must fail the build, not corrupt memory.
 * Layout verified with offsetof against the C++ struct of 1.4.7. */
_Static_assert(sizeof(struct RendererConfig) == 232, "RendererConfig size changed");
_Static_assert(offsetof(struct RendererConfig, configVersion) == 0, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, penWhitelist) == 1, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, penBlacklist) == 21, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, useWhitelist) == 41, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, disabledLayers) == 48, "RendererConfig layout changed");
_Static_assert(sizeof(RmlCrdtId) == 16, "CrdtId layout changed");
_Static_assert(offsetof(struct RendererConfig, enableText) == 208, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, enableImages) == 209, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, enableGlyphHighlights) == 210, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, enableBackdrop) == 211, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, useBackdropForSamplingOnly) == 212, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, followRulesInJson) == 213, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, strokerDataInJson) == 214, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, backdropOffsetX) == 216, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, backdropOffsetY) == 220, "RendererConfig layout changed");
_Static_assert(offsetof(struct RendererConfig, backdropAlign) == 224, "RendererConfig layout changed");
